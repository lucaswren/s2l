package dnsproxy

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/dnswire"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/speedtest"
)

func TestResolverDeadlinePreservesKnownSOCKSStage(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "tcp"})
	defer r.Close()
	r.exchange = func(ctx context.Context, _ string, _ []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, &speedtest.StageError{Stage: "proxy-connect", Err: ctx.Err()}
	}
	query, _ := QueryA("example.com")
	if _, err := r.Resolve(context.Background(), query); err == nil {
		t.Fatal("timed-out query unexpectedly succeeded")
	}
	if failure := r.Status().LastTCPFailure; failure == nil || failure.Stage != "proxy-connect" {
		t.Fatalf("deadline hid the known SOCKS phase: %+v", failure)
	}
}

func TestCanceledRequestsDoNotDisablePrimary(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	defer r.Close()
	r.exchange = func(ctx context.Context, _ string, _ []byte) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }
	q, _ := QueryA("example.com")
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := r.Resolve(ctx, q); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if s := r.Status(); s.Active != "tcp" || s.Switches != 0 || s.Failures != 0 {
		t.Fatal("cancellation counted as upstream failure", s)
	}
}

func TestBackupDoesNotWaitForPrimaryRecoveryProbe(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	defer r.Close()
	r.status.Active = "doh"
	started := make(chan struct{})
	r.exchange = func(ctx context.Context, method string, q []byte) ([]byte, error) {
		if method == "tcp" {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return response(q, 0), nil
	}
	q, _ := QueryA("example.com")
	done := make(chan error, 1)
	go func() { _, err := r.Resolve(context.Background(), q); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("working backup blocked by failed primary probe")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("primary recovery was never checked")
	}
	closed := make(chan struct{})
	go func() { r.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel background recovery")
	}
}

func TestDoHFailureRestoresTCPFromSuccessfulLiveQueries(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	defer r.Close()
	r.status.Active = "doh"
	r.nextProbe = time.Now().Add(time.Hour)
	var dohCalls atomic.Int32
	r.exchange = func(_ context.Context, method string, q []byte) ([]byte, error) {
		if method == "doh" {
			dohCalls.Add(1)
			return nil, errors.New("DoH unavailable")
		}
		return response(q, 0), nil
	}
	q, _ := QueryA("example.com")
	for _, active := range []string{"doh", "tcp", "tcp"} {
		if _, err := r.Resolve(context.Background(), q); err != nil {
			t.Fatal(err)
		}
		if s := r.Status(); s.Active != active {
			t.Fatalf("active transport %q, want %q", s.Active, active)
		}
	}
	if s, calls := r.Status(), dohCalls.Load(); calls != 2 || s.Switches != 1 || s.Fallbacks != 2 {
		t.Fatalf("recovered TCP should handle the next query directly: DoH calls=%d, status=%+v", calls, s)
	}
}

func TestStalledDoHRestoresTCPFromSuccessfulLiveQueries(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	defer r.Close()
	r.status.Active = "doh"
	var dohCalls atomic.Int32
	r.exchange = func(ctx context.Context, method string, q []byte) ([]byte, error) {
		if method == "doh" {
			dohCalls.Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return response(q, 0), nil
	}
	q, _ := QueryA("example.com")
	for _, want := range []string{"doh", "tcp", "tcp"} {
		if _, err := r.Resolve(context.Background(), q); err != nil {
			t.Fatal(err)
		}
		if got := r.Status().Active; got != want {
			t.Fatalf("active transport %q, want %q", got, want)
		}
	}
	if s := r.Status(); dohCalls.Load() != 2 || s.Fallbacks != 2 || s.Switches != 1 {
		t.Fatalf("recovered TCP did not handle the next request directly: %+v", s)
	}
}

func TestTCPPoolWaitDoesNotSwitchToDoH(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	defer r.Close()
	r.nextProbe = time.Now().Add(time.Hour)
	r.exchange = func(_ context.Context, method string, q []byte) ([]byte, error) {
		if method == "tcp" {
			return nil, errTCPPoolWait
		}
		return response(q, 0), nil
	}
	q, _ := QueryA("example.com")
	for i := 0; i < 3; i++ {
		if _, err := r.Resolve(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	if s := r.Status(); s.Active != "tcp" || s.Fallbacks != 3 || s.Switches != 0 || s.LastTCPFailure != nil {
		t.Fatalf("local connection pool congestion was treated as an upstream failure: %+v", s)
	}
}

func TestCallerDeadlineDoesNotRecordTCPFailure(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "tcp"})
	defer r.Close()
	r.exchange = func(ctx context.Context, _ string, _ []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	q, _ := QueryA("example.com")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.Resolve(ctx, q); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if s := r.Status(); s.Failures != 0 || s.LastTCPFailure != nil {
		t.Fatalf("caller deadline was recorded as an upstream failure: %+v", s)
	}
}

func TestMalformedAnswerCannotBypassFallback(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	defer r.Close()
	q, _ := QueryA("example.com")
	methods := []string{}
	r.exchange = func(_ context.Context, method string, q []byte) ([]byte, error) {
		methods = append(methods, method)
		a := response(q, 0)
		if method == "tcp" {
			a[7] = 1
		}
		return a, nil
	}
	if _, err := r.Resolve(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || methods[1] != "doh" {
		t.Fatal("malformed primary response did not trigger fallback", methods)
	}
}

func TestCompressionPointerCyclesAreRejected(t *testing.T) {
	packet := []byte{0xc0, 0}
	if _, err := dnswire.SkipName(packet, 0); err == nil {
		t.Fatal("cyclic DNS compression pointer accepted")
	}
}

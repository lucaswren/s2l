package dnsproxy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func TestCanceledRequestsDoNotDisablePrimary(t *testing.T) {
	r := New(model.SocksNode{}, "auto")
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
	if s := r.Status(); s.Active != "doh" || s.Switches != 0 || s.Failures != 0 {
		t.Fatal("cancellation counted as upstream failure", s)
	}
}

func TestBackupDoesNotWaitForPrimaryRecoveryProbe(t *testing.T) {
	r := New(model.SocksNode{}, "auto")
	defer r.Close()
	r.status.Active = "tcp"
	started := make(chan struct{})
	r.exchange = func(ctx context.Context, method string, q []byte) ([]byte, error) {
		if method == "doh" {
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

func TestMalformedAnswerCannotBypassFallback(t *testing.T) {
	r := New(model.SocksNode{}, "auto")
	defer r.Close()
	q, _ := QueryA("example.com")
	methods := []string{}
	r.exchange = func(_ context.Context, method string, q []byte) ([]byte, error) {
		methods = append(methods, method)
		a := response(q, 0)
		if method == "doh" {
			a[7] = 1
		}
		return a, nil
	}
	if _, err := r.Resolve(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || methods[1] != "tcp" {
		t.Fatal("malformed primary response did not trigger fallback", methods)
	}
}

func TestCompressionPointerCyclesAreRejected(t *testing.T) {
	packet := []byte{0xc0, 0}
	if _, err := skipName(packet, 0); err == nil {
		t.Fatal("cyclic DNS compression pointer accepted")
	}
}

package dnsproxy

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func TestAutoDNSRacesStalledTransportInBothDirections(t *testing.T) {
	for _, preferred := range []string{"doh", "tcp"} {
		t.Run(preferred, func(t *testing.T) {
			r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
			defer r.Close()
			r.status.Active = preferred
			r.nextProbe = time.Now().Add(time.Hour)
			cancelled := make(chan struct{}, 1)
			r.exchange = func(ctx context.Context, method string, q []byte) ([]byte, error) {
				if method == preferred {
					<-ctx.Done()
					cancelled <- struct{}{}
					return nil, ctx.Err()
				}
				return response(q, 0), nil
			}
			q, _ := QueryA("example.com")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := r.Resolve(ctx, q); err != nil {
				t.Fatal("working alternate waited for preferred timeout", err)
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("losing request was not cancelled")
			}
			if s := r.Status(); s.Failures != 0 || s.Fallbacks != 1 {
				t.Fatal(s)
			}
		})
	}
}

func TestAutoDNSFastAnswerDoesNotSendBackup(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	var calls atomic.Int32
	r.exchange = func(_ context.Context, method string, q []byte) ([]byte, error) {
		calls.Add(1)
		if method != "tcp" {
			return nil, errors.New("unexpected alternate")
		}
		return response(q, 3), nil // Valid NXDOMAIN must not trigger fallback.
	}
	q, _ := QueryA("missing.example.com")
	if a, err := r.Resolve(context.Background(), q); err != nil || a[3]&15 != 3 {
		t.Fatal(a, err)
	}
	r.Close()
	if calls.Load() != 1 || r.Status().Fallbacks != 0 {
		t.Fatal("fast valid answer launched an alternate", calls.Load(), r.Status())
	}
}

func TestAutoDNSFailedTCPImmediatelyRetriesDoH(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	defer r.Close()
	r.status.Active = "tcp"
	r.nextProbe = time.Now().Add(time.Hour)
	r.exchange = func(_ context.Context, method string, q []byte) ([]byte, error) {
		if method == "tcp" {
			return nil, errors.New("primary refused")
		}
		return response(q, 0), nil
	}
	q, _ := QueryA("example.com")
	ctx, cancel := context.WithTimeout(context.Background(), fallbackDelay/2)
	defer cancel()
	if _, err := r.Resolve(ctx, q); err != nil {
		t.Fatal("immediate failure waited for hedge timer", err)
	}
	if s := r.Status(); s.Fallbacks != 1 || s.Failures != 0 || s.Active != "tcp" {
		t.Fatal("fallback must preserve recovery hysteresis", s)
	}
	s := r.Status()
	if s.LastDoHFailure != nil || s.LastTCPFailure == nil ||
		s.LastTCPFailure.Endpoint != model.DefaultDNSTCPAddress || s.LastTCPFailure.Stage != "exchange" {
		t.Fatal("primary transport failure must remain visible after successful fallback", s)
	}
}

func TestAutoDNSBothFailCountOneQueryFailure(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	defer r.Close()
	r.exchange = func(context.Context, string, []byte) ([]byte, error) {
		return nil, errors.New("upstream unavailable")
	}
	q, _ := QueryA("example.com")
	for i := 0; i < 3; i++ {
		if _, err := r.Resolve(context.Background(), q); err == nil {
			t.Fatal("both failures hidden")
		}
	}
	if s := r.Status(); s.Queries != 3 || s.Failures != 3 || s.Fallbacks != 0 || s.Switches != 0 || s.Active != "tcp" {
		t.Fatal(s)
	}
}

func TestAutoDNSShutdownCancelsHedgedRequests(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	defer r.Close()
	started := make(chan struct{}, 2)
	r.exchange = func(ctx context.Context, _ string, _ []byte) ([]byte, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	q, _ := QueryA("example.com")
	done := make(chan error, 1)
	go func() { _, err := r.Resolve(context.Background(), q); done <- err }()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("alternate not started")
		}
	}
	r.Close()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("resolver shutdown left a pending request")
	}
	if s := r.Status(); s.Failures != 0 || s.Switches != 0 {
		t.Fatal("shutdown counted as upstream failure", s)
	}
}
func TestRepeatedFastBackupDoesNotSwitchPrimary(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	defer r.Close()
	r.nextProbe = time.Now().Add(time.Hour)
	r.exchange = func(ctx context.Context, method string, q []byte) ([]byte, error) {
		if method == "tcp" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return response(q, 0), nil
	}
	q, _ := QueryA("example.com")
	for i := 0; i < 3; i++ {
		if _, err := r.Resolve(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	if s := r.Status(); s.Active != "tcp" || s.Switches != 0 || s.Fallbacks != 3 || s.LastTCPFailure != nil {
		t.Fatalf("backup speed was treated as confirmed TCP DNS failure: %+v", s)
	}
}

func TestAutoDNSExtendedErrorFallsBack(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	defer r.Close()
	r.exchange = func(_ context.Context, method string, query []byte) ([]byte, error) {
		answer := response(query, 0)
		if method == "tcp" {
			answer[11] = 1
			// OPT with extended RCODE 1 and header RCODE 0 gives BADVERS (16).
			return append(answer, 0, 0, 41, 4, 208, 1, 0, 0, 0, 0, 0), nil
		}
		return answer, nil
	}
	query, err := QueryA("example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(context.Background(), query); err != nil {
		t.Fatal(err)
	}
	if status := r.Status(); status.Fallbacks != 1 || status.LastTCPFailure == nil {
		t.Fatalf("BADVERS did not trigger fallback: %+v", status)
	}
}

func TestDNSOPTOptionLength(t *testing.T) {
	query, err := QueryA("example.com")
	if err != nil {
		t.Fatal(err)
	}
	answer := response(query, 0)
	answer[11] = 1
	// An unknown, well-formed option is allowed. Its length must fit the OPT record.
	answer = append(answer, 0, 0, 41, 4, 208, 0, 0, 0, 0, 0, 5, 0xff, 0xfe, 0, 1, 0)
	if err := ValidateResponse(query, answer); err != nil {
		t.Fatal("valid OPT rejected", err)
	}
	answer[len(answer)-2] = 2
	if err := ValidateResponse(query, answer); err == nil {
		t.Fatal("accepted OPT option extending past the end of the record")
	}
}

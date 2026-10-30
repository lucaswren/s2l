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
		if method != "doh" {
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
			return nil, errors.New("backup refused")
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
}

func TestAutoDNSBothFailCountOneQueryFailure(t *testing.T) {
	r := New(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	defer r.Close()
	r.exchange = func(context.Context, string, []byte) ([]byte, error) {
		return nil, errors.New("upstream unavailable")
	}
	q, _ := QueryA("example.com")
	if _, err := r.Resolve(context.Background(), q); err == nil {
		t.Fatal("both failures hidden")
	}
	if s := r.Status(); s.Queries != 1 || s.Failures != 1 || s.Fallbacks != 0 {
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

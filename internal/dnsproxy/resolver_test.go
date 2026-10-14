package dnsproxy

import (
	"context"
	"errors"
	"github.com/lucaswren/s2l/internal/model"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func response(q []byte, rcode byte) []byte {
	a := append([]byte(nil), q...)
	a[2] |= 0x80
	a[3] = 0x80 | rcode
	return a
}
func TestFallbackCooldownAndHystereticRecovery(t *testing.T) {
	r := New(model.SocksNode{}, "auto")
	defer r.Close()
	q, _ := QueryA("images.example.com")
	var primaryOK atomic.Bool
	var callsMu sync.Mutex
	calls := []string{}
	r.exchange = func(_ context.Context, method string, q []byte) ([]byte, error) {
		callsMu.Lock()
		calls = append(calls, method)
		callsMu.Unlock()
		if method == "doh" && !primaryOK.Load() {
			return nil, errors.New("primary down")
		}
		return response(q, 0), nil
	}
	for i := 0; i < 2; i++ {
		if _, err := r.Resolve(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	if s := r.Status(); s.Active != "tcp" || s.Fallbacks != 2 || s.Switches != 1 {
		t.Fatal(s)
	}
	callsMu.Lock()
	calls = nil
	callsMu.Unlock()
	if _, err := r.Resolve(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	callsMu.Lock()
	callsCopy := append([]string(nil), calls...)
	callsMu.Unlock()
	if len(callsCopy) != 1 || callsCopy[0] != "tcp" {
		t.Fatal("cooldown retried failed primary", callsCopy)
	}
	primaryOK.Store(true)
	for i := 0; i < 2; i++ {
		r.mu.Lock()
		r.nextProbe = time.Time{}
		r.mu.Unlock()
		if _, err := r.Resolve(context.Background(), q); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for {
			r.mu.Lock()
			probing := r.probing
			r.mu.Unlock()
			if !probing {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("primary probe did not finish")
			}
			time.Sleep(time.Millisecond)
		}
		if i == 0 && r.Status().Active != "tcp" {
			t.Fatal("one success prematurely restored primary")
		}
	}
	if r.Status().Active != "doh" {
		t.Fatal("primary did not recover")
	}
}
func TestNegativeAnswersAndInvalidResponses(t *testing.T) {
	q, _ := QueryA("missing.example.com")
	if err := ValidateResponse(q, response(q, 3)); err != nil {
		t.Fatal("NXDOMAIN should not trigger failover", err)
	}
	for _, a := range [][]byte{response(q, 2), response(q, 5), {0, 1}, response(q, 0)} {
		if len(a) > 12 && a[3]&15 == 0 {
			a[0] ^= 1
		}
		if ValidateResponse(q, a) == nil {
			t.Fatal("accepted failed or mismatched DNS response")
		}
	}
	a := response(q, 0)
	a[2] |= 2
	if ValidateResponse(q, a) == nil {
		t.Fatal("accepted truncated TCP response")
	}
}
func TestForcedModeNeverUsesOtherTransport(t *testing.T) {
	for _, mode := range []string{"doh", "tcp"} {
		t.Run(mode, func(t *testing.T) {
			r := New(model.SocksNode{}, mode)
			defer r.Close()
			calls := 0
			r.exchange = func(_ context.Context, method string, _ []byte) ([]byte, error) {
				calls++
				if method != mode {
					t.Fatal(method)
				}
				return nil, errors.New("unavailable")
			}
			q, _ := QueryA("example.com")
			if _, err := r.Resolve(context.Background(), q); err == nil {
				t.Fatal("failure hidden")
			}
			if calls != 1 {
				t.Fatal(calls)
			}
		})
	}
}

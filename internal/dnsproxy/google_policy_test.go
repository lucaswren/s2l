package dnsproxy

import (
	"context"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func httpsQuery(t *testing.T, host string) []byte {
	t.Helper()
	q, err := QueryA(host)
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint16(q[len(q)-4:], 65)
	return q
}
func TestGoogleHTTPSPolicyBoundariesAndDNSSEC(t *testing.T) {
	for _, host := range []string{"google.com", "ACCOUNTS.Google.Com", "gmail.googleapis.com", "www.youtube.com", "rr.googlevideo.com", "a.ytimg.com", "x.gstatic.com", "x.googleusercontent.com"} {
		q := httpsQuery(t, host)
		b, ok := googleHTTPSNoData(q)
		if !ok || ValidateResponse(q, b) != nil || b[3]&15 != 0 || binary.BigEndian.Uint16(b[6:8]) != 0 || b[2]&0x84 != 0x80 || b[3]&0x20 != 0 {
			t.Fatalf("%s: %x", host, b)
		}
	}
	for _, host := range []string{"evilgoogle.com", "google.com.evil.example", "example.com", "notgoogleapis.com"} {
		if _, ok := googleHTTPSNoData(httpsQuery(t, host)); ok {
			t.Fatal(host)
		}
	}
	base := httpsQuery(t, "gmail.googleapis.com")
	variants := map[string]func([]byte) []byte{
		"A":          func(q []byte) []byte { binary.BigEndian.PutUint16(q[len(q)-4:], 1); return q },
		"AAAA":       func(q []byte) []byte { binary.BigEndian.PutUint16(q[len(q)-4:], 28); return q },
		"AD":         func(q []byte) []byte { q[3] = 0x20; return q },
		"CD":         func(q []byte) []byte { q[3] = 0x10; return q },
		"answer":     func(q []byte) []byte { q[7] = 1; return q },
		"trailing":   func(q []byte) []byte { return append(q, 0) },
		"class":      func(q []byte) []byte { q[len(q)-1] = 3; return q },
		"TSIG":       func(q []byte) []byte { q[11] = 1; return append(q, 0, 0, 250, 0, 1, 0, 0, 0, 0, 0, 0) },
		"DO":         func(q []byte) []byte { q[11] = 1; return append(q, 0, 0, 41, 4, 208, 0, 0, 128, 0, 0, 0) },
		"bad-option": func(q []byte) []byte { q[11] = 1; return append(q, 0, 0, 41, 4, 208, 0, 0, 0, 0, 0, 1, 0) },
	}
	for name, alter := range variants {
		q := alter(append([]byte(nil), base...))
		if _, ok := googleHTTPSNoData(q); ok {
			t.Fatal(name)
		}
	}
	q := append([]byte(nil), base...)
	q[11] = 1
	q = append(q, 0, 0, 41, 4, 208, 0, 0, 0, 0, 0, 4, 0, 12, 0, 0)
	if b, ok := googleHTTPSNoData(q); !ok || ValidateResponse(q, b) != nil || len(b) != len(base)+11 {
		t.Fatalf("EDNS: %x %v", b, ok)
	}
	for n := 0; n < len(base); n++ {
		if _, ok := googleHTTPSNoData(base[:n]); ok {
			t.Fatalf("truncated %d", n)
		}
	}
}

func TestMappingPolicyKeepsTransportAndCountsConcurrentRequests(t *testing.T) {
	for _, policy := range []string{"reject", "reject-quic", "allow", ""} {
		s, err := Start(model.SocksNode{}, "tcp", policy)
		if err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int64
		s.Resolver.exchange = func(_ context.Context, _ string, q []byte) ([]byte, error) { calls.Add(1); return response(q, 0), nil }
		q := httpsQuery(t, "gmail.googleapis.com")
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := s.Resolver.Resolve(context.Background(), q); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		blocked := policy == "reject" || policy == "reject-quic"
		if (calls.Load() == 0) != blocked || (s.Resolver.Status().GoogleHTTPSSuppressed == 20) != blocked {
			t.Fatalf("%s: %+v calls=%d", policy, s.Resolver.Status(), calls.Load())
		}
		// Ordinary DNS remains on the selected TCP resolver.
		a, _ := QueryA("gmail.googleapis.com")
		before := calls.Load()
		if _, err := s.Resolver.Resolve(context.Background(), a); err != nil || calls.Load() != before+1 {
			t.Fatal(policy, err)
		}
		s.Close()
		if _, err := s.Resolver.Resolve(context.Background(), q); err == nil {
			t.Fatal("closed resolver answered")
		}
	}
}

package dnsproxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func TestHTTPSRecordsPassThroughLiveListener(t *testing.T) {
	for _, mode := range []string{"auto", "doh", "tcp"} {
		t.Run(mode, func(t *testing.T) {
			s, err := Start(model.SocksNode{}, model.DNSOptions{Mode: mode})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			for _, host := range []string{"gemini.google.com", "gmail.googleapis.com", "oauthaccountmanager.googleapis.com", "example.com"} {
				for _, kind := range []uint16{1, 28, 65} {
					for _, empty := range []bool{false, true} {
						q, _ := QueryA(host)
						binary.BigEndian.PutUint16(q[len(q)-4:], kind)
						expected := response(q, 0)
						if !empty {
							data := []byte{203, 0, 113, 8}
							if kind == 28 {
								data = make([]byte, 16)
								data[15] = 1
							}
							if kind == 65 {
								data = []byte{0, 1, 0, 0, 1, 0, 6, 2, 'h', '2', 2, 'h', '3', 0, 5, 0, 3, 1, 2, 3}
							}
							expected[7] = 1
							expected = append(expected, 0xc0, 12, byte(kind>>8), byte(kind), 0, 1, 0, 0, 0, 30, 0, byte(len(data)))
							expected = append(expected, data...)
						}
						s.Resolver.mu.Lock()
						s.Resolver.exchange = func(_ context.Context, method string, got []byte) ([]byte, error) {
							if !bytes.Equal(got, q) || (mode == "tcp" && method != "tcp") || (mode == "doh" && method != "doh") {
								t.Errorf("query or transport changed")
							}
							return expected, nil
						}
						s.Resolver.mu.Unlock()
						got, err := s.Query(context.Background(), q)
						if err != nil || !bytes.Equal(got, expected) {
							t.Fatalf("%s type %d: %x %v", host, kind, got, err)
						}
					}
				}
			}
			q, _ := QueryA("gmail.googleapis.com")
			s.Resolver.mu.Lock()
			s.Resolver.exchange = func(_ context.Context, _ string, q []byte) ([]byte, error) { return response(q, 0), nil }
			s.Resolver.mu.Unlock()
			s.listener.Close()
			if _, err := s.Resolver.Resolve(context.Background(), q); err != nil {
				t.Fatal("control resolver unavailable", err)
			}
			if _, err := s.Query(context.Background(), q); err == nil {
				t.Fatal("stopped listener was bypassed")
			}
		})
	}
}

package dnsproxy

import (
	"context"
	"encoding/binary"
	"github.com/lucaswren/s2l/internal/model"
	"io"
	"net"
	"testing"
	"time"
)

func TestLocalTCPResolverFramesAndCancellation(t *testing.T) {
	s, err := Start(model.SocksNode{}, model.DNSOptions{Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	s.Resolver.mu.Lock()
	s.Resolver.exchange = func(_ context.Context, _ string, q []byte) ([]byte, error) { return response(q, 0), nil }
	s.Resolver.mu.Unlock()
	conn, err := net.Dial("tcp", s.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 2; i++ {
		q, _ := QueryA("images.example.com")
		size := make([]byte, 2)
		binary.BigEndian.PutUint16(size, uint16(len(q)))
		conn.Write(append(size, q...))
		if _, err := io.ReadFull(conn, size); err != nil {
			t.Fatal(err)
		}
		a := make([]byte, binary.BigEndian.Uint16(size))
		if _, err := io.ReadFull(conn, a); err != nil {
			t.Fatal(err)
		}
		if err := ValidateResponse(q, a); err != nil {
			t.Fatal(err)
		}
	}
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("resolver shutdown blocked on idle client")
	}
}

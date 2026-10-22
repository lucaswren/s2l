package dnsproxy

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestTCPPoolReuseStaleReconnectAndClose(t *testing.T) {
	var dials atomic.Int32
	p := &tcpPool{slots: make(chan struct{}, 4)}
	p.dial = func(context.Context) (net.Conn, error) {
		dials.Add(1)
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			for n := 0; n < 2; n++ {
				var size [2]byte
				if _, e := io.ReadFull(server, size[:]); e != nil {
					return
				}
				q := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, e := io.ReadFull(server, q); e != nil {
					return
				}
				a := answerA(q)
				binary.BigEndian.PutUint16(size[:], uint16(len(a)))
				if _, e := server.Write(append(size[:], a...)); e != nil {
					return
				}
			}
		}()
		return client, nil
	}
	q, _ := QueryA("example.com")
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		a, e := p.query(ctx, q)
		cancel()
		if e != nil || ValidateResponse(q, a) != nil {
			t.Fatal(i, e)
		}
	}
	if dials.Load() != 3 {
		t.Fatal("connections not reused or stale connection not replaced", dials.Load())
	}
	p.close()
	if _, e := p.query(context.Background(), q); e == nil {
		t.Fatal("closed pool accepted query")
	}
}
func TestTCPPoolCancellationReleasesCapacity(t *testing.T) {
	p := &tcpPool{slots: make(chan struct{}, 1), dial: func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go func() { defer b.Close(); io.Copy(io.Discard, b) }()
		return a, nil
	}}
	defer p.close()
	q, _ := QueryA("example.com")
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		_, e := p.query(ctx, q)
		cancel()
		if e == nil {
			t.Fatal("cancellation ignored")
		}
	}
	if len(p.slots) != 0 {
		t.Fatal("leaked connection lease")
	}
}

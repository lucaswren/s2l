package dnsproxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func answerA(q []byte) []byte {
	a := response(q, 0)
	a[7] = 1
	return append(a, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 30, 0, 4, 1, 1, 1, 1)
}

// The fixture permits only its own loopback DNS endpoints and records every
// SOCKS CONNECT, so a direct DNS fallback would fail the forwarding assertion.
func TestRealTransportsAndFallbackStayOnSOCKS(t *testing.T) {
	var tcpQueries atomic.Int32
	tcpListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tcpListener.Close() })
	go func() {
		for {
			c, e := tcpListener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				var size [2]byte
				if _, e := io.ReadFull(c, size[:]); e != nil {
					return
				}
				q := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, e := io.ReadFull(c, q); e != nil {
					return
				}
				tcpQueries.Add(1)
				a := answerA(q)
				binary.BigEndian.PutUint16(size[:], uint16(len(a)))
				c.Write(append(size[:], a...))
			}()
		}
	}()
	var primaryWorks atomic.Bool
	var dohQueries atomic.Int32
	doh := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dohQueries.Add(1)
		if !primaryWorks.Load() {
			w.WriteHeader(503)
			return
		}
		q, _ := io.ReadAll(io.LimitReader(r.Body, 65536))
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(answerA(q))
	}))
	defer doh.Close()
	proxy, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	dohAddr := doh.Listener.Addr().String()
	tcpAddr := tcpListener.Addr().String()
	var mu sync.Mutex
	targets := map[string]int{}
	go func() {
		for {
			c, e := proxy.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				g := make([]byte, 3)
				if _, e := io.ReadFull(c, g); e != nil {
					return
				}
				if g[0] != 5 || g[1] != 1 || g[2] != 0 {
					return
				}
				c.Write([]byte{5, 0})
				h := make([]byte, 4)
				if _, e := io.ReadFull(c, h); e != nil {
					return
				}
				if h[0] != 5 || h[1] != 1 || h[3] != 1 {
					return
				}
				address := make([]byte, 6)
				if _, e := io.ReadFull(c, address); e != nil {
					return
				}
				target := net.JoinHostPort(net.IP(address[:4]).String(), fmt.Sprint(binary.BigEndian.Uint16(address[4:])))
				if target != dohAddr && target != tcpAddr {
					return
				}
				mu.Lock()
				targets[target]++
				mu.Unlock()
				remote, e := net.DialTimeout("tcp", target, time.Second)
				if e != nil {
					return
				}
				defer remote.Close()
				remote.SetDeadline(time.Now().Add(5 * time.Second))
				c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				done := make(chan struct{})
				go func() { io.Copy(remote, c); remote.Close(); close(done) }()
				io.Copy(c, remote)
				c.Close()
				<-done
			}()
		}
	}()
	r := newResolver(model.SocksNode{Addr: proxy.Addr().String()}, "auto", doh.URL+"/dns-query", tcpAddr)
	defer r.Close()
	r.transport.TLSClientConfig = doh.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	q, _ := QueryA("images.example.com")
	for i := 0; i < 2; i++ {
		a, e := r.Resolve(context.Background(), q)
		if e != nil {
			t.Fatal(e)
		}
		ips, e := IPv4Answers(q, a)
		if e != nil || len(ips) != 1 || ips[0].String() != "1.1.1.1" {
			t.Fatal("real DNS response was not forwarded", e)
		}
	}
	if s := r.Status(); s.Active != "tcp" || s.Fallbacks != 2 {
		t.Fatal(s)
	}
	primaryWorks.Store(true)
	for i := 0; i < 2; i++ {
		r.mu.Lock()
		r.nextProbe = time.Time{}
		r.mu.Unlock()
		if _, e := r.Resolve(context.Background(), q); e != nil {
			t.Fatal(e)
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			r.mu.Lock()
			probing := r.probing
			r.mu.Unlock()
			if !probing {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("primary transport failed to recover")
			}
			time.Sleep(time.Millisecond)
		}
	}
	if r.Status().Active != "doh" {
		t.Fatal("live primary did not recover")
	}
	mu.Lock()
	tcpConnects, dohConnects := targets[tcpAddr], targets[dohAddr]
	mu.Unlock()
	if int(tcpQueries.Load()) != tcpConnects || tcpConnects < 4 || dohConnects < 1 || dohQueries.Load() < 4 {
		t.Fatalf("DNS bypassed SOCKS: TCP queries=%d CONNECTs=%d DoH CONNECTs=%d", tcpQueries.Load(), tcpConnects, dohConnects)
	}
}

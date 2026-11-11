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
	var primaryWorks atomic.Bool
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
				a := response(q, 2)
				if primaryWorks.Load() {
					a = answerA(q)
				}
				binary.BigEndian.PutUint16(size[:], uint16(len(a)))
				c.Write(append(size[:], a...))
			}()
		}
	}()
	var dohQueries atomic.Int32
	var dohHTTP2 atomic.Int32
	var dohStalls atomic.Bool
	doh := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dohQueries.Add(1)
		if r.ProtoMajor == 2 {
			dohHTTP2.Add(1)
		}
		q, _ := io.ReadAll(io.LimitReader(r.Body, 65536))
		if dohStalls.Load() {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(answerA(q))
	}))
	doh.EnableHTTP2 = true
	doh.StartTLS()
	defer doh.Close()
	proxy, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	dohAddr := doh.Listener.Addr().String()
	tcpAddr := tcpListener.Addr().String()
	_, dohPort, _ := net.SplitHostPort(dohAddr)
	_, tcpPort, _ := net.SplitHostPort(tcpAddr)
	// These names are resolved only inside the SOCKS fixture. The .invalid
	// destination cannot accidentally succeed through the host resolver.
	dohName := net.JoinHostPort("example.com", dohPort)
	tcpName := net.JoinHostPort("resolver.example.invalid", tcpPort)
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
				if h[0] != 5 || h[1] != 1 || h[3] != 3 {
					return
				}
				var size [1]byte
				if _, e := io.ReadFull(c, size[:]); e != nil {
					return
				}
				address := make([]byte, int(size[0])+2)
				if _, e := io.ReadFull(c, address); e != nil {
					return
				}
				target := net.JoinHostPort(string(address[:size[0]]), fmt.Sprint(binary.BigEndian.Uint16(address[size[0]:])))
				upstream := map[string]string{dohName: dohAddr, tcpName: tcpAddr}[target]
				if upstream == "" {
					return
				}
				mu.Lock()
				targets[target]++
				mu.Unlock()
				remote, e := net.DialTimeout("tcp", upstream, time.Second)
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
	s, err := Start(model.SocksNode{Addr: proxy.Addr().String()}, model.DNSOptions{Mode: "auto", DoHURL: "https://" + dohName + "/dns-query", TCPAddress: tcpName})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := s.Resolver
	r.transport.TLSClientConfig = doh.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	q, _ := QueryA("images.example.com")
	for i := 0; i < 2; i++ {
		a, e := s.Query(context.Background(), q)
		if e != nil {
			t.Fatal(e)
		}
		ips, e := IPv4Answers(q, a)
		if e != nil || len(ips) != 1 || ips[0].String() != "1.1.1.1" {
			t.Fatal("real DNS response was not forwarded", e)
		}
	}
	if s := r.Status(); s.Active != "doh" || s.Fallbacks != 2 {
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
	if r.Status().Active != "tcp" {
		t.Fatal("live primary did not recover")
	}
	mu.Lock()
	tcpConnects, dohConnects := targets[tcpName], targets[dohName]
	mu.Unlock()
	// Check the primary failures and recovery probes via SOCKS, and verify
	// the DoH backup negotiated HTTP/2 over the same proxy.
	if int(tcpQueries.Load()) != tcpConnects || tcpConnects < 4 || dohConnects < 1 || dohQueries.Load() < 2 || dohHTTP2.Load() < 2 {
		t.Fatalf("DNS transport path mismatch: TCP queries=%d CONNECTs=%d DoH CONNECTs=%d requests=%d HTTP/2=%d", tcpQueries.Load(), tcpConnects, dohConnects, dohQueries.Load(), dohHTTP2.Load())
	}
	primaryWorks.Store(false)
	for i := 0; i < 2; i++ {
		if _, err := s.Query(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.Status().Active; got != "doh" {
		t.Fatalf("TCP outage did not switch to DoH: %s", got)
	}
	dohStalls.Store(true)
	primaryWorks.Store(true)
	for _, want := range []string{"doh", "tcp"} {
		if _, err := s.Query(context.Background(), q); err != nil {
			t.Fatal("working TCP did not answer while DoH stalled", err)
		}
		if got := r.Status().Active; got != want {
			t.Fatalf("active transport after live TCP reply = %s, want %s", got, want)
		}
	}
	// Even if both actual resolvers are directly reachable on this host, an
	// unavailable SOCKS upstream must never trigger a direct DNS connection.
	proxy.Close()
	blocked, err := Start(model.SocksNode{Addr: proxy.Addr().String()}, model.DNSOptions{DoHURL: doh.URL + "/dns-query", TCPAddress: tcpAddr})
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()
	blocked.Resolver.transport.TLSClientConfig = doh.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	beforeTCP, beforeDoH := tcpQueries.Load(), dohQueries.Load()
	if _, err := blocked.Query(context.Background(), q); err == nil {
		t.Fatal("DNS succeeded with unavailable SOCKS upstream")
	}
	status := blocked.Resolver.Status()
	for _, failure := range []*model.DNSAttemptFailure{status.LastDoHFailure, status.LastTCPFailure} {
		if failure == nil || failure.Stage != "proxy-connect" || failure.Endpoint == "" {
			t.Fatalf("missing DNS proxy-connect failure: %+v", failure)
		}
	}
	if tcpQueries.Load() != beforeTCP || dohQueries.Load() != beforeDoH {
		t.Fatal("failed SOCKS connection fell back to direct DNS")
	}
}

package diagnostics

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
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

type resolverFunc func(context.Context, []byte) ([]byte, error)

func (f resolverFunc) Resolve(c context.Context, q []byte) ([]byte, error) { return f(c, q) }
func answer(q []byte, rcode byte, ip net.IP) []byte {
	b := append([]byte(nil), q...)
	b[2] = 0x81
	b[3] = 0x80 | rcode
	if ip == nil {
		return b
	}
	b[7] = 1
	kind := binary.BigEndian.Uint16(q[len(q)-4:])
	v := ip.To4()
	if kind == 28 {
		v = ip.To16()
	}
	b = append(b, 0xc0, 12, byte(kind>>8), byte(kind), 0, 1, 0, 0, 0, 30, 0, byte(len(v)))
	return append(b, v...)
}
func TestDNSAddressFamiliesAndEmptyAnswers(t *testing.T) {
	for _, kind := range []uint16{1, 28} {
		r := resolverFunc(func(_ context.Context, q []byte) ([]byte, error) {
			return answer(q, 0, net.ParseIP("2001:db8::1")), nil
		})
		if kind == 1 {
			r = func(_ context.Context, q []byte) ([]byte, error) {
				return answer(q, 0, net.ParseIP("203.0.113.8")), nil
			}
		}
		got := resolve(context.Background(), r, "accounts.google.com", kind)
		if got.State != "healthy" || len(got.Addresses) != 1 {
			t.Fatal(got)
		}
	}
	for _, rc := range []byte{0, 3} {
		r := resolverFunc(func(_ context.Context, q []byte) ([]byte, error) { return answer(q, rc, nil), nil })
		v := resolve(context.Background(), r, "x.example", 1)
		if rc == 0 && v.State != "no_address" || rc == 3 && v.State != "nxdomain" {
			t.Fatal(v)
		}
	}
	for n := 0; n < 30; n++ {
		_ = addresses(make([]byte, n), 1)
	}
}

func TestSuiteFallbackPinnedExitIsolationAndCancellation(t *testing.T) {
	var calls atomic.Int32
	var nodes sync.Map
	doh := resolverFunc(func(context.Context, []byte) ([]byte, error) { return nil, errors.New("offline") })
	tcp := resolverFunc(func(_ context.Context, q []byte) ([]byte, error) {
		if binary.BigEndian.Uint16(q[len(q)-4:]) == 28 {
			return answer(q, 0, nil), nil
		}
		return answer(q, 0, net.ParseIP("203.0.113.9")), nil
	})
	probe := func(_ context.Context, n model.SocksNode, ip string, row *Target) {
		if ip != "203.0.113.9" {
			t.Error(ip)
		}
		nodes.Store(n.ID, true)
		calls.Add(1)
		row.TCP = "healthy"
		row.TLS = "healthy"
		row.HTTP = "responded"
		row.Status = 403
	}
	exit := func(_ context.Context, n model.SocksNode) model.ProtocolHealth {
		nodes.Store(n.ID, true)
		return model.ProtocolHealth{ExitIP: "203.0.113.10"}
	}
	r := google(context.Background(), model.SocksNode{ID: "fixed"}, []string{"accounts.google.com", "gmail.googleapis.com"}, doh, tcp, probe, exit)
	if calls.Load() != 2 || r.Targets[0].DOH.State != "unavailable" || r.Targets[0].TCPDNS.State != "healthy" || r.Targets[0].Status != 403 || r.Targets[0].AAAA.State != "no_address" {
		t.Fatal(r)
	}
	nodes.Range(func(k, v any) bool {
		if k != "fixed" {
			t.Error(k)
		}
		return true
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls.Store(0)
	blocked := resolverFunc(func(c context.Context, _ []byte) ([]byte, error) { return nil, c.Err() })
	r = google(ctx, model.SocksNode{ID: "fixed"}, []string{"accounts.google.com"}, blocked, blocked, probe, exit)
	if calls.Load() != 0 || r.Targets[0].TCP != "not_tested" {
		t.Fatal(r)
	}
}

// The proxy accepts only CONNECT to the DNS-selected numeric IPv4 and port 443.
// Its sole upstream is a local TLS fixture, so no external service is required.
func socksFixture(t *testing.T, upstream string) string {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				b := make([]byte, 3)
				if _, e := io.ReadFull(c, b); e != nil {
					return
				}
				c.Write([]byte{5, 0})
				h := make([]byte, 10)
				if _, e := io.ReadFull(c, h); e != nil {
					return
				}
				if h[1] != 1 || h[3] != 1 || !net.IP(h[4:8]).Equal(net.ParseIP("203.0.113.9")) || binary.BigEndian.Uint16(h[8:]) != 443 {
					t.Error("unexpected SOCKS destination", h)
					return
				}
				u, e := net.DialTimeout("tcp", upstream, time.Second)
				if e != nil {
					return
				}
				defer u.Close()
				c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
				done := make(chan struct{})
				go func() { io.Copy(u, c); close(done) }()
				io.Copy(c, u)
				c.Close()
				u.Close()
				<-done
			}()
		}
	}()
	return l.Addr().String()
}
func TestPinnedTLSHTTP2AndCertificateFailure(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "example.com" {
			t.Error(r.Host)
		}
		w.WriteHeader(403)
		w.Write([]byte("restricted"))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	node := model.SocksNode{Addr: socksFixture(t, server.Listener.Addr().String())}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	row := Target{Host: "example.com", TCP: "not_tested", TLS: "not_tested", HTTP: "not_tested"}
	probeTLS(context.Background(), node, "203.0.113.9", &row, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if row.TCP != "healthy" || row.TLS != "healthy" || row.HTTP != "responded" || row.Status != 403 || row.ALPN != "h2" {
		t.Fatalf("%+v", row)
	}
	row = Target{Host: "example.com", TCP: "not_tested", TLS: "not_tested", HTTP: "not_tested"}
	probe(context.Background(), node, "203.0.113.9", &row)
	if row.TCP != "healthy" || row.TLS != "unavailable" || row.HTTP != "not_tested" || row.Stage != "tls" {
		t.Fatalf("%+v", row)
	}
	if row.ErrorCode != "certificate_error" || row.Error == "" {
		t.Fatalf("missing certificate diagnosis: %+v", row)
	}
}

func TestGoogleAddressRetriesKeepExitAndFailureEvidence(t *testing.T) {
	row := Target{Host: "gmail.googleapis.com", DOH: DNSResult{Addresses: []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"}}, TCPDNS: DNSResult{Addresses: []string{"192.0.2.8", "192.0.2.1"}}}
	var ips []string
	probeAddresses(context.Background(), model.SocksNode{ID: "fixed"}, &row, func(_ context.Context, node model.SocksNode, ip string, target *Target) {
		if node.ID != "fixed" || target.Host != "gmail.googleapis.com" {
			t.Fatal("exit or SNI changed")
		}
		ips = append(ips, ip)
		target.TCP = "healthy"
		target.Stage = "tls"
		target.TLS = "unavailable"
		target.ErrorCode = "peer_closed"
		target.Error = "EOF"
		if ip == "192.0.2.8" {
			target.TLS = "healthy"
			target.HTTP = "responded"
			target.Status = 404
			target.ErrorCode = ""
			target.Error = ""
		}
	})
	if len(ips) != 2 || ips[0] != "192.0.2.1" || ips[1] != "192.0.2.8" || row.Status != 404 || row.IP != "192.0.2.8" || len(row.Attempts) != 2 || row.Attempts[0].Error != "EOF" || len(row.DOH.Addresses) != 3 {
		t.Fatalf("%+v %v", row, ips)
	}
	row = Target{Host: "gmail.googleapis.com", DOH: DNSResult{Addresses: []string{"192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4"}}, TCPDNS: DNSResult{Addresses: []string{"192.0.2.1", "192.0.2.2"}}}
	probeAddresses(context.Background(), model.SocksNode{}, &row, func(_ context.Context, _ model.SocksNode, ip string, target *Target) {
		target.TCP = "healthy"
		target.TLS = "unavailable"
		target.Stage = "tls"
	})
	if len(row.Attempts) != 3 || row.TCP != "healthy" || row.TLS != "unavailable" {
		t.Fatalf("unbounded or lost stage: %+v", row)
	}
	code, message := probeError(errors.New("tls: peer secret-user private-password"), model.SocksNode{Username: "secret-user", Password: "private-password"})
	if code != "tls_error" || message != "tls: peer [redacted] [redacted]" {
		t.Fatal(code, message)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	row = Target{Host: "gmail.googleapis.com", DOH: DNSResult{Addresses: []string{"192.0.2.1"}}}
	probeAddresses(ctx, model.SocksNode{}, &row, func(context.Context, model.SocksNode, string, *Target) { t.Fatal("cancelled check dialed") })
	if !row.Cancelled || row.Stage != "cancelled" || len(row.Attempts) != 0 {
		t.Fatalf("cancelled result lost: %+v", row)
	}
}

func TestTLSDeadlinePreservesFailedStage(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		c, e := listener.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(time.Second))
		io.Copy(io.Discard, c)
	}()
	node := model.SocksNode{Addr: socksFixture(t, listener.Addr().String())}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	row := Target{Host: "example.com", TCP: "not_tested", TLS: "not_tested", HTTP: "not_tested"}
	probe(ctx, node, "203.0.113.9", &row)
	if !row.TimedOut || row.Stage != "tls" || row.TCP != "healthy" || row.HTTP != "not_tested" {
		t.Fatalf("%+v", row)
	}
}

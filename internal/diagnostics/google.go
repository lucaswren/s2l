// Package diagnostics provides explicit, read-only service checks, never failover.
package diagnostics

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/dnsproxy"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/speedtest"
)

var googleHosts = []string{"www.google.com", "accounts.google.com", "oauth2.googleapis.com", "gmail.googleapis.com", "www.youtube.com", "gemini.google.com", "robinfrontend-pa.googleapis.com", "oauthaccountmanager.googleapis.com"}

type DNSResult struct {
	State      string   `json:"state"`
	Addresses  []string `json:"addresses"`
	RCode      int      `json:"rcode"`
	DurationMS int64    `json:"duration_ms"`
}
type Target struct {
	Host       string    `json:"host"`
	A          DNSResult `json:"a"`
	DOH        DNSResult `json:"doh"`
	TCPDNS     DNSResult `json:"tcp_dns"`
	AAAA       DNSResult `json:"aaaa"`
	TCP        string    `json:"tcp"`
	TLS        string    `json:"tls"`
	HTTP       string    `json:"http"`
	Status     int       `json:"status"`
	ALPN       string    `json:"alpn"`
	TCPMS      int64     `json:"tcp_ms"`
	TLSMS      int64     `json:"tls_ms"`
	TimedOut   bool      `json:"timed_out"`
	Cancelled  bool      `json:"cancelled"`
	Stage      string    `json:"stage"`
	DurationMS int64     `json:"duration_ms"`
}
type Report struct {
	NodeID          string               `json:"node_id"`
	NodeName        string               `json:"node_name"`
	CheckedAt       time.Time            `json:"checked_at"`
	Exit            model.ProtocolHealth `json:"exit"`
	Targets         []Target             `json:"targets"`
	Scope           string               `json:"scope"`
	QUIC            string               `json:"quic"`
	TCPAlternatives int                  `json:"tcp_alternatives"`
	ClientFallback  string               `json:"client_fallback"`
}

// Google resolves through this node only, then pins each HTTPS dial to the
// returned IPv4 address. No host DNS, alternate node or UDP is used.
func Google(ctx context.Context, node model.SocksNode) Report {
	doh, tcp := dnsproxy.New(node, "doh"), dnsproxy.New(node, "tcp")
	defer doh.Close()
	defer tcp.Close()
	return google(ctx, node, googleHosts, doh, tcp, probe, speedtest.ProbeExitIP)
}

type resolver interface {
	Resolve(context.Context, []byte) ([]byte, error)
}

func google(ctx context.Context, node model.SocksNode, hosts []string, doh, tcp resolver, probeTarget func(context.Context, model.SocksNode, string, *Target), exit func(context.Context, model.SocksNode) model.ProtocolHealth) Report {
	r := Report{NodeID: node.ID, NodeName: node.Name, CheckedAt: time.Now(), Targets: make([]Target, len(hosts)),
		Scope: "服务器 → 固定 SOCKS5；独立 DoH/TCP DNS，解析所得首个 IPv4 → TCP/TLS/HTTP。未经过 Panabit/手机，不验证账号登录或 App 全部接口。",
		QUIC:  "未发送 UDP/QUIC；TCP/TLS 成功仅证明存在 TCP 访问路径，不证明 iPhone 已回退。需对比 Panabit 手机入站侧拒绝 UDP/443 前后结果，并核对 ICMP 返回。"}

	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Add(1)
		go func(i int, host string) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				r.Targets[i] = Target{Host: host, TCP: "not_tested", TLS: "not_tested", HTTP: "not_tested", Stage: "cancelled", A: DNSResult{State: "cancelled", Addresses: []string{}}, AAAA: DNSResult{State: "cancelled", Addresses: []string{}}, DOH: DNSResult{State: "cancelled", Addresses: []string{}}, TCPDNS: DNSResult{State: "cancelled", Addresses: []string{}}}
				return
			}
			row := Target{Host: host, TCP: "not_tested", TLS: "not_tested", HTTP: "not_tested"}
			row.DOH = resolve(ctx, doh, host, 1)
			row.TCPDNS = resolve(ctx, tcp, host, 1)
			row.A = row.DOH
			if row.A.State != "healthy" {
				row.A = row.TCPDNS
			}
			row.AAAA = resolve(ctx, doh, host, 28)
			if row.AAAA.State == "unavailable" {
				row.AAAA = resolve(ctx, tcp, host, 28)
			}
			if len(row.A.Addresses) > 0 {
				probeTarget(ctx, node, row.A.Addresses[0], &row)
			} else {
				row.Stage = "dns"
			}
			r.Targets[i] = row
		}(i, host)
	}
	wg.Wait()
	r.ClientFallback = "unverified"
	for _, row := range r.Targets {
		if row.TCP == "healthy" && row.TLS == "healthy" {
			r.TCPAlternatives++
		}
	}
	if ctx.Err() == nil {
		r.Exit = exit(ctx, node)
	}
	return r
}

func resolve(ctx context.Context, r resolver, host string, kind uint16) DNSResult {
	start := time.Now()
	result := DNSResult{State: "unavailable", Addresses: []string{}}
	q, err := dnsproxy.QueryA(host)
	if err != nil {
		return result
	}
	binary.BigEndian.PutUint16(q[len(q)-4:], kind)
	answer, err := r.Resolve(ctx, q)
	result.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		if ctx.Err() != nil {
			result.State = "cancelled"
		}
		return result
	}
	result.RCode = int(answer[3] & 15)
	if result.RCode != 0 {
		result.State = "nxdomain"
		return result
	}
	// Validated DNS names can be compressed. Walk records rather than treating
	// the total answer count (which includes CNAMEs) as an address count.
	result.Addresses = addresses(answer, kind)
	result.State = "no_address"
	if len(result.Addresses) > 0 {
		result.State = "healthy"
	}
	return result
}
func skipName(b []byte, p int) int {
	for p < len(b) {
		n := int(b[p])
		p++
		if n == 0 {
			return p
		}
		if n&0xc0 == 0xc0 {
			if p < len(b) {
				return p + 1
			}
			return -1
		}
		if n > 63 || p+n > len(b) {
			return -1
		}
		p += n
	}
	return -1
}
func addresses(b []byte, kind uint16) []string {
	out := []string{}
	if len(b) < 12 {
		return out
	}
	p := skipName(b, 12)
	if p < 0 || p+4 > len(b) {
		return out
	}
	p += 4
	for i := 0; i < int(binary.BigEndian.Uint16(b[6:8])); i++ {
		p = skipName(b, p)
		if p < 0 || p+10 > len(b) {
			return out
		}
		k, c, n := binary.BigEndian.Uint16(b[p:]), binary.BigEndian.Uint16(b[p+2:]), int(binary.BigEndian.Uint16(b[p+8:]))
		p += 10
		if p+n > len(b) {
			return out
		}
		if k == kind && c == 1 && ((kind == 1 && n == 4) || (kind == 28 && n == 16)) {
			out = append(out, net.IP(b[p:p+n]).String())
		}
		p += n
	}
	return out
}

func probe(ctx context.Context, node model.SocksNode, ip string, row *Target) {
	probeTLS(ctx, node, ip, row, nil)
}
func probeTLS(ctx context.Context, node model.SocksNode, ip string, row *Target, config *tls.Config) {
	original := row
	local := *row
	row = &local
	var mu sync.Mutex
	var tlsStart time.Time
	defer func() { mu.Lock(); *original = local; mu.Unlock() }()
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	row.TCP = "unavailable"
	row.Stage = "target-connect"
	tr := &http.Transport{Proxy: nil, TLSClientConfig: config, ForceAttemptHTTP2: true, DisableKeepAlives: true, MaxResponseHeaderBytes: 32 << 10,
		DialContext: func(c context.Context, _, _ string) (net.Conn, error) {
			dialStart := time.Now()
			conn, err := speedtest.DialSOCKS5(c, node.Addr, node.Username, node.Password, net.JoinHostPort(ip, "443"))
			mu.Lock()
			defer mu.Unlock()
			row.TCPMS = time.Since(dialStart).Milliseconds()
			if err == nil {
				row.TCP = "healthy"
			} else {
				row.Stage = speedtest.ErrorStage(err)
			}
			return conn, err
		}}
	defer tr.CloseIdleConnections()
	trace := &httptrace.ClientTrace{TLSHandshakeStart: func() {
		mu.Lock()
		defer mu.Unlock()
		row.TLS = "unavailable"
		row.Stage = "tls"
		tlsStart = time.Now()
	}, TLSHandshakeDone: func(s tls.ConnectionState, e error) {
		mu.Lock()
		defer mu.Unlock()
		if !tlsStart.IsZero() {
			row.TLSMS = time.Since(tlsStart).Milliseconds()
		}
		if e == nil {
			row.TLS = "healthy"
			row.ALPN = s.NegotiatedProtocol
			row.Stage = "http"
			row.HTTP = "unavailable"
		}
	}}
	req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "GET", "https://"+row.Host+"/", nil)
	client := http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	mu.Lock()
	defer mu.Unlock()
	if resp != nil {
		row.Status = resp.StatusCode
		_, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if err == nil && readErr == nil {
			row.HTTP = "responded"
		}
	}
	row.Cancelled = errors.Is(ctx.Err(), context.Canceled)
	row.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	row.DurationMS = time.Since(start).Milliseconds()
}

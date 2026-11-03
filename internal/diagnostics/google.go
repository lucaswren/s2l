// Package diagnostics provides explicit, read-only service checks, never failover.
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
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/dnsproxy"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/speedtest"
)

var googleHosts = []string{"www.google.com", "accounts.google.com", "oauth2.googleapis.com", "gmail.googleapis.com", "www.youtube.com", "gemini.google.com", "robinfrontend-pa.googleapis.com", "oauthaccountmanager.googleapis.com"}

type DNSResult struct {
	Records    int      `json:"records"`
	State      string   `json:"state"`
	Addresses  []string `json:"addresses"`
	RCode      int      `json:"rcode"`
	DurationMS int64    `json:"duration_ms"`
}
type Target struct {
	HTTPS      DNSResult      `json:"https"`
	IP         string         `json:"ip,omitempty"`
	ErrorCode  string         `json:"error_code,omitempty"`
	Error      string         `json:"error,omitempty"`
	Attempts   []ProbeAttempt `json:"attempts,omitempty"`
	Host       string         `json:"host"`
	A          DNSResult      `json:"a"`
	DOH        DNSResult      `json:"doh"`
	TCPDNS     DNSResult      `json:"tcp_dns"`
	AAAA       DNSResult      `json:"aaaa"`
	TCP        string         `json:"tcp"`
	TLS        string         `json:"tls"`
	HTTP       string         `json:"http"`
	Status     int            `json:"status"`
	ALPN       string         `json:"alpn"`
	TCPMS      int64          `json:"tcp_ms"`
	TLSMS      int64          `json:"tls_ms"`
	TimedOut   bool           `json:"timed_out"`
	Cancelled  bool           `json:"cancelled"`
	Stage      string         `json:"stage"`
	DurationMS int64          `json:"duration_ms"`
}
type ProbeAttempt struct {
	IP         string `json:"ip"`
	Stage      string `json:"stage"`
	TCP        string `json:"tcp"`
	TLS        string `json:"tls"`
	HTTP       string `json:"http"`
	Status     int    `json:"status"`
	ALPN       string `json:"alpn,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}
type Report struct {
	MappingID       string               `json:"mapping_id,omitempty"`
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

// Healthy is server-side reachability only, never proof of client fallback.
func (r Report) Healthy() bool {
	if len(r.Targets) == 0 {
		return false
	}
	validDNS := func(v DNSResult) bool {
		return v.State == "healthy" || v.State == "no_address" || v.State == "nxdomain"
	}
	for _, t := range r.Targets {
		if t.A.State != "healthy" || !validDNS(t.AAAA) || !validDNS(t.HTTPS) || t.TCP != "healthy" || t.TLS != "healthy" || t.HTTP != "responded" {
			return false
		}
	}
	return true
}

// Google resolves through this node only, then pins each HTTPS dial to the
// returned IPv4 address. No host DNS, alternate node or UDP is used.
func Google(ctx context.Context, node model.SocksNode) Report {
	doh, tcp := dnsproxy.New(node, model.DNSOptions{Mode: "doh"}), dnsproxy.New(node, model.DNSOptions{Mode: "tcp"})
	defer doh.Close()
	defer tcp.Close()
	return google(ctx, node, googleHosts, doh, tcp, probe, speedtest.ProbeExitIP)
}

type resolver interface {
	Resolve(context.Context, []byte) ([]byte, error)
}

type ResolverFunc func(context.Context, []byte) ([]byte, error)

func (f ResolverFunc) Resolve(ctx context.Context, q []byte) ([]byte, error) { return f(ctx, q) }

// GoogleMapping uses the running mapping's DNS listener and only its selected
// upstream. TCP/TLS still starts at the server, not at a real PPP client.
func GoogleMapping(ctx context.Context, node model.SocksNode, id string, lookup ResolverFunc) Report {
	r := google(ctx, node, googleHosts, nil, nil, probe, func(context.Context, model.SocksNode) model.ProtocolHealth {
		return model.ProtocolHealth{State: "not_tested"}
	}, lookup)
	r.MappingID = id
	r.Scope = "映射 DNS 监听器 → 固定节点 → 目标；TCP/TLS 从服务器发起，未验证手机、PPP 转发与账号登录。"
	return r
}

func google(ctx context.Context, node model.SocksNode, hosts []string, doh, tcp resolver, probeTarget func(context.Context, model.SocksNode, string, *Target), exit func(context.Context, model.SocksNode) model.ProtocolHealth, mapping ...resolver) Report {
	r := Report{NodeID: node.ID, NodeName: node.Name, CheckedAt: time.Now(), Targets: make([]Target, len(hosts)),
		Scope: "服务器经固定节点检查 DNS、TCP/TLS、HTTP；失败时最多复测 3 个 IPv4 地址。不验证客户端连接和账号登录。",
		QUIC:  "未发送 QUIC；TCP/TLS 可用不代表客户端已回退，需核对客户端行为与 ICMP 返回。"}

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
				r.Targets[i] = Target{Host: host, TCP: "not_tested", TLS: "not_tested", HTTP: "not_tested", Stage: "cancelled", A: DNSResult{State: "cancelled", Addresses: []string{}}, AAAA: DNSResult{State: "cancelled", Addresses: []string{}}, HTTPS: DNSResult{State: "cancelled", Addresses: []string{}}, DOH: DNSResult{State: "cancelled", Addresses: []string{}}, TCPDNS: DNSResult{State: "cancelled", Addresses: []string{}}}
				return
			}
			row := Target{Host: host, TCP: "not_tested", TLS: "not_tested", HTTP: "not_tested"}
			if len(mapping) > 0 {
				row.DOH = DNSResult{State: "not_tested", Addresses: []string{}}
				row.TCPDNS = row.DOH
				row.A = resolve(ctx, mapping[0], host, 1)
				row.AAAA = resolve(ctx, mapping[0], host, 28)
				row.HTTPS = resolve(ctx, mapping[0], host, 65)
			} else {
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
				row.HTTPS = resolve(ctx, doh, host, 65)
				if row.HTTPS.State == "unavailable" {
					row.HTTPS = resolve(ctx, tcp, host, 65)
				}
			}
			if len(row.A.Addresses) > 0 {
				if len(mapping) > 0 {
					probeAddressSources(ctx, node, &row, probeTarget, row.A)
				} else {
					probeAddresses(ctx, node, &row, probeTarget)
				}
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

// Retry destination addresses only within this node. DNS answers are interleaved
// so the independent TCP resolver gets a chance before a third DoH address.
func probeAddresses(ctx context.Context, node model.SocksNode, row *Target, check func(context.Context, model.SocksNode, string, *Target)) {
	probeAddressSources(ctx, node, row, check, row.DOH, row.TCPDNS)
}
func probeAddressSources(ctx context.Context, node model.SocksNode, row *Target, check func(context.Context, model.SocksNode, string, *Target), sources ...DNSResult) {
	var ips []string
	seen := map[string]bool{}
	for i := 0; len(ips) < 3; i++ {
		added := false
		for _, result := range sources {
			if i >= len(result.Addresses) {
				continue
			}
			added = true
			ip := result.Addresses[i]
			parsed := net.ParseIP(ip)
			if !seen[ip] && parsed != nil && parsed.To4() != nil {
				seen[ip] = true
				ips = append(ips, ip)
				if len(ips) == 3 {
					break
				}
			}
		}
		if !added {
			break
		}
	}
	var attempts []ProbeAttempt
	best := Target{Host: row.Host, TCP: "not_tested", TLS: "not_tested", HTTP: "not_tested"}
	budget, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	started := time.Now()
	for _, ip := range ips {
		if budget.Err() != nil {
			break
		}
		attemptCtx, done := context.WithTimeout(budget, 4*time.Second)
		trial := Target{Host: row.Host, IP: ip, TCP: "not_tested", TLS: "not_tested", HTTP: "not_tested"}
		check(attemptCtx, node, ip, &trial)
		done()
		attempts = append(attempts, ProbeAttempt{IP: ip, Stage: trial.Stage, TCP: trial.TCP, TLS: trial.TLS, HTTP: trial.HTTP, Status: trial.Status, ALPN: trial.ALPN, ErrorCode: trial.ErrorCode, Error: trial.Error, DurationMS: trial.DurationMS})
		if len(attempts) == 1 || probeProgress(trial) >= probeProgress(best) {
			best = trial
		}
		if trial.HTTP == "responded" {
			break
		}
	}
	best.A, best.AAAA, best.DOH, best.TCPDNS = row.A, row.AAAA, row.DOH, row.TCPDNS
	best.HTTPS = row.HTTPS
	best.Attempts = attempts
	if best.HTTP != "responded" {
		best.Cancelled = best.Cancelled || errors.Is(budget.Err(), context.Canceled)
		best.TimedOut = best.TimedOut || errors.Is(budget.Err(), context.DeadlineExceeded)
		if len(attempts) == 0 && budget.Err() != nil {
			best.Stage = "cancelled"
		}
	}
	best.DurationMS = time.Since(started).Milliseconds()
	*row = best
}
func probeProgress(row Target) int {
	if row.HTTP == "responded" {
		return 3
	}
	if row.TLS == "healthy" {
		return 2
	}
	if row.TCP == "healthy" {
		return 1
	}
	return 0
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
	if err == nil {
		err = dnsproxy.ValidateResponse(q, answer)
	}
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
	result.Records = recordCount(answer, kind)
	result.State = "no_address"
	if len(result.Addresses) > 0 || (kind == 65 && result.Records > 0) {
		result.State = "healthy"
	}
	return result
}
func recordCount(b []byte, kind uint16) int {
	p := skipName(b, 12)
	if p < 0 || p+4 > len(b) {
		return 0
	}
	p += 4
	count := 0
	for i := 0; i < int(binary.BigEndian.Uint16(b[6:8])); i++ {
		p = skipName(b, p)
		if p < 0 || p+10 > len(b) {
			return count
		}
		k, c, n := binary.BigEndian.Uint16(b[p:]), binary.BigEndian.Uint16(b[p+2:]), int(binary.BigEndian.Uint16(b[p+8:]))
		p += 10
		if p+n > len(b) {
			return count
		}
		if k == kind && c == 1 {
			count++
		}
		p += n
	}
	return count
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
		if err == nil {
			err = readErr
		}
	}
	if err != nil {
		row.ErrorCode, row.Error = probeError(err, node)
	}
	row.Cancelled = errors.Is(ctx.Err(), context.Canceled)
	row.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	row.DurationMS = time.Since(start).Milliseconds()
}

func probeError(err error, node model.SocksNode) (string, string) {
	code := "connection_error"
	var certificate *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var network net.Error
	switch {
	case errors.As(err, &certificate) || errors.As(err, &unknown) || errors.As(err, &hostname):
		code = "certificate_error"
	case errors.Is(err, context.DeadlineExceeded):
		code = "timeout"
	case errors.Is(err, context.Canceled):
		code = "cancelled"
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		code = "peer_closed"
	case errors.As(err, &network) && network.Timeout():
		code = "timeout"
	case strings.Contains(strings.ToLower(err.Error()), "reset"):
		code = "connection_reset"
	case strings.Contains(err.Error(), "tls:"):
		code = "tls_error"
	}
	message := err.Error()
	for _, secret := range []string{node.Username, node.Password} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	message = strings.ToValidUTF8(message, "?")
	runes := []rune(message)
	if len(runes) > 512 {
		message = string(runes[:512]) + "…"
	}
	return code, message
}

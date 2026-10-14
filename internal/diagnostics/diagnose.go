// Package diagnostics performs bounded, explicit target checks via the chosen SOCKS5.
package diagnostics

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/dnsproxy"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/speedtest"
)

type Stage struct {
	State      string `json:"state"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}
type Result struct {
	Target      string   `json:"target"`
	DNS         Stage    `json:"dns"`
	SOCKS       Stage    `json:"socks"`
	TLS         Stage    `json:"tls"`
	HTTP        Stage    `json:"http"`
	Download    Stage    `json:"download"`
	Addresses   []string `json:"addresses"`
	StatusCode  int      `json:"status_code"`
	ContentType string   `json:"content_type"`
	Bytes       int64    `json:"bytes"`
	Limited     bool     `json:"limited"`
	Scope       string   `json:"scope"`
}

func ParseTarget(target string) (*url.URL, error) {
	if len(target) > 2048 {
		return nil, fmt.Errorf("URL is too long")
	}
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("use an http/https URL without credentials or fragment")
	}
	if u.Port() != "" && u.Port() != "80" && u.Port() != "443" {
		return nil, fmt.Errorf("diagnostics only supports ports 80 and 443")
	}
	return u, nil
}
func publicIPv4(ip net.IP) bool {
	if ip.To4() == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/3"} {
		_, subnet, _ := net.ParseCIDR(cidr)
		if subnet.Contains(ip) {
			return false
		}
	}
	return true
}

type ResolveFunc func(context.Context, []byte) ([]byte, error)

func Diagnose(ctx context.Context, node model.SocksNode, target string, resolve ResolveFunc) (Result, error) {
	return diagnose(ctx, node, target, resolve, &tls.Config{MinVersion: tls.VersionTLS12})
}

func diagnose(ctx context.Context, node model.SocksNode, target string, resolve ResolveFunc, tlsConfig *tls.Config) (Result, error) {
	u, err := ParseTarget(target)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	sanitized := *u
	sanitized.RawQuery = ""
	sanitized.ForceQuery = false
	result := Result{Target: sanitized.String(), Scope: "SOCKS5 IPv4 path; compare with the real L2TP client", Addresses: []string{}, DNS: Stage{State: "skipped"}, SOCKS: Stage{State: "skipped"}, TLS: Stage{State: "skipped"}, HTTP: Stage{State: "skipped"}, Download: Stage{State: "skipped"}}
	var ips []net.IP
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		ips = []net.IP{ip}
	} else {
		if resolve == nil {
			return result, fmt.Errorf("DNS resolver unavailable")
		}
		start := time.Now()
		query, err := dnsproxy.QueryA(u.Hostname())
		if err != nil {
			return result, err
		}
		response, err := resolve(ctx, query)
		if err == nil {
			ips, err = dnsproxy.IPv4Answers(query, response)
		}
		result.DNS = Stage{State: "healthy", DurationMs: time.Since(start).Milliseconds()}
		if err != nil || len(ips) == 0 {
			result.DNS.State = "failed"
			result.DNS.Error = "DNS failed or returned no IPv4 address"
			return result, nil
		}
	}
	for _, ip := range ips {
		if !publicIPv4(ip) {
			return result, fmt.Errorf("diagnostics requires public IPv4 destinations")
		}
		if len(result.Addresses) < 8 {
			result.Addresses = append(result.Addresses, ip.String())
		}
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	var mu sync.Mutex
	var tlsStart, httpStart time.Time
	var socksStage, tlsStage Stage
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, TLSHandshakeTimeout: 7 * time.Second, ResponseHeaderTimeout: 8 * time.Second, MaxResponseHeaderBytes: 64 * 1024,
		TLSClientConfig: tlsConfig.Clone(),
		DialContext: func(c context.Context, _, _ string) (net.Conn, error) {
			start := time.Now()
			var conn net.Conn
			var err error
			for _, ip := range result.Addresses {
				attempt, stop := context.WithTimeout(c, 5*time.Second)
				conn, err = speedtest.DialSOCKS5(attempt, node.Addr, node.Username, node.Password, net.JoinHostPort(ip, port))
				stop()
				if err == nil {
					break
				}
				if c.Err() != nil {
					break
				}
			}
			mu.Lock()
			socksStage = Stage{State: "healthy", DurationMs: time.Since(start).Milliseconds()}
			if err != nil {
				socksStage.State = "failed"
				socksStage.Error = "SOCKS5 authentication or target connection failed"
			}
			mu.Unlock()
			return conn, err
		}}
	defer transport.CloseIdleConnections()
	trace := &httptrace.ClientTrace{TLSHandshakeStart: func() { mu.Lock(); tlsStart = time.Now(); mu.Unlock() }, TLSHandshakeDone: func(_ tls.ConnectionState, e error) {
		mu.Lock()
		defer mu.Unlock()
		tlsStage = Stage{State: "healthy", DurationMs: time.Since(tlsStart).Milliseconds()}
		if e != nil {
			tlsStage.State = "failed"
			tlsStage.Error = "TLS handshake or certificate verification failed"
		}
	}, WroteRequest: func(info httptrace.WroteRequestInfo) { mu.Lock(); httpStart = time.Now(); mu.Unlock() }}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, u.String(), nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("User-Agent", "S2L-Diagnostics/1.0")
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	mu.Lock()
	result.SOCKS = socksStage
	if result.SOCKS.State == "" {
		result.SOCKS.State = "failed"
		result.SOCKS.Error = "Connection canceled before SOCKS5 completed"
	}
	if tlsStage.State != "" {
		result.TLS = tlsStage
	}
	requestWrittenAt := httpStart
	mu.Unlock()
	if err != nil {
		mu.Lock()
		if socksStage.State == "healthy" && result.TLS.State != "failed" {
			result.HTTP = Stage{State: "failed", Error: "HTTP request failed or timed out"}
		}
		mu.Unlock()
		return result, nil
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	result.ContentType = resp.Header.Get("Content-Type")
	result.HTTP = Stage{State: "healthy"}
	if !requestWrittenAt.IsZero() {
		result.HTTP.DurationMs = time.Since(requestWrittenAt).Milliseconds()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.HTTP.State = "failed"
		result.HTTP.Error = fmt.Sprintf("HTTP %d; redirects are not followed", resp.StatusCode)
	}
	start := time.Now()
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 2*1024*1024+1))
	result.Bytes = n
	result.Limited = n > 2*1024*1024
	result.Download = Stage{State: "healthy", DurationMs: time.Since(start).Milliseconds()}
	if result.Limited {
		result.Download.State = "limited"
	} else if err != nil {
		result.Download.State = "failed"
		result.Download.Error = "Response body was interrupted"
	}
	return result, nil
}

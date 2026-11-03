package speedtest

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

var healthTargets = []string{"https://www.cloudflare.com/cdn-cgi/trace", "https://www.google.com/generate_204"}

// ProbeHTTPS checks one administrator-selected host:port. Results are kept
// separate from automatic node health; redirects are never followed.
func ProbeHTTPS(ctx context.Context, node model.SocksNode, target string) model.ProtocolHealth {
	return probeHTTPSTargets(ctx, node, []string{"https://" + target + "/"})
}

// CheckHealth retains the strict success contract used by existing callers.
func CheckHealth(ctx context.Context, node model.SocksNode) error {
	return checkHealthTargets(ctx, node, healthTargets)
}
func checkHealthTargets(ctx context.Context, node model.SocksNode, targets []string) error {
	r := probeHTTPSTargets(ctx, node, targets)
	if r.State == "healthy" {
		return nil
	}
	return fmt.Errorf("%s", r.Error)
}

// Sequential fallback avoids doubling healthy-node traffic. Every leg is bounded
// and cancellable; target HTTP restrictions never become a proxy-auth verdict.
func probeHTTPSTargets(ctx context.Context, node model.SocksNode, targets []string) model.ProtocolHealth {
	started := time.Now()
	report := model.ProtocolHealth{State: "unavailable", Method: "https"}
	var response *model.ProtocolHealth
	for _, target := range targets {
		if ctx.Err() != nil {
			break
		}
		r := probeHTTPS(ctx, target, func(c context.Context, addr string) (net.Conn, error) {
			return DialSOCKS5(c, node.Addr, node.Username, node.Password, addr)
		})
		u, _ := url.Parse(target)
		host := ""
		if u != nil {
			host = u.Hostname()
		}
		report.Attempts = append(report.Attempts, model.ProbeAttempt{Target: host, Stage: r.Stage, StatusCode: r.StatusCode, DurationMS: r.DurationMS})
		if r.State == "healthy" {
			r.Attempts = report.Attempts
			r.DurationMS = time.Since(started).Milliseconds()
			return r
		}
		if r.State == "restricted" {
			copy := r
			response = &copy
		}
		report.Stage, report.Error, report.DurationMS = r.Stage, r.Error, r.DurationMS
	}
	if response != nil {
		response.Attempts = report.Attempts
		response.DurationMS = time.Since(started).Milliseconds()
		return *response
	}
	report.CheckedAt = time.Now()
	report.DurationMS = time.Since(started).Milliseconds()
	if ctx.Err() != nil {
		report.Stage = "cancelled"
		report.Error = "检测超时或已取消"
	}
	if report.Error == "" {
		report.Error = "没有可用的检测目标"
	}
	return report
}

func probeHTTPS(ctx context.Context, target string, dial func(context.Context, string) (net.Conn, error)) model.ProtocolHealth {
	start := time.Now()
	attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var phase atomic.Value
	phase.Store("target-connect")
	trace := &httptrace.ClientTrace{TLSHandshakeStart: func() { phase.Store("tls") }, TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
		if err == nil {
			phase.Store("http")
		}
	}}
	attempt = httptrace.WithClientTrace(attempt, trace)
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 32 << 10,
		DialContext: func(c context.Context, _, addr string) (net.Conn, error) { return dial(c, addr) }}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r := model.ProtocolHealth{State: "unavailable", Method: "https"}
	req, err := http.NewRequestWithContext(attempt, http.MethodGet, target, nil)
	if err == nil {
		var resp *http.Response
		resp, err = client.Do(req)
		if err == nil {
			r.StatusCode = resp.StatusCode
			phase.Store("http")
			_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if err == nil {
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					r.State = "healthy"
				} else {
					r.State = "restricted"
					r.Error = fmt.Sprintf("目标已响应 HTTP %d", resp.StatusCode)
				}
			}
		}
	}
	stage := phase.Load().(string)
	if err != nil {
		var se *StageError
		if errors.As(err, &se) {
			stage = se.Stage
		}
		descriptions := map[string]string{"proxy-connect": "节点连接失败或超时", "socks-auth": "SOCKS5 协商或认证失败", "target-connect": "目标连接失败或超时", "tls": "TLS 握手或证书校验失败", "http": "HTTP 响应读取失败"}
		r.Error = descriptions[stage]
	}
	r.Stage = stage
	r.DurationMS = time.Since(start).Milliseconds()
	r.CheckedAt = time.Now()
	return r
}

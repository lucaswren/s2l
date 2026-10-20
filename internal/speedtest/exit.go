package speedtest

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

// A fixed numeric HTTPS endpoint keeps DNS and arbitrary URL input out of the
// exit proof. A changing exit is evidence, never an automatic failover trigger.
func ProbeExitIP(ctx context.Context, node model.SocksNode) model.ProtocolHealth {
	return probeExitTarget(ctx, node, "https://1.1.1.1/cdn-cgi/trace")
}
func probeExitTarget(ctx context.Context, node model.SocksNode, target string) model.ProtocolHealth {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result := model.ProtocolHealth{State: "unavailable", Method: "socks-https-exit"}
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 32 << 10, DialContext: func(c context.Context, _, addr string) (net.Conn, error) {
		return DialSOCKS5(c, node.Addr, node.Username, node.Password, addr)
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return model.ProtocolHealth{State: "unavailable", Error: "出口检测目标无效", CheckedAt: time.Now()}
	}
	response, err := client.Do(req)
	if err == nil {
		result.StatusCode = response.StatusCode
		data, e := io.ReadAll(io.LimitReader(response.Body, 4097))
		response.Body.Close()
		if e == nil && len(data) <= 4096 && response.StatusCode == 200 {
			if ip := ParseExitIP(string(data)); ip != "" {
				result.State = "healthy"
				result.ExitIP = ip
			}
		}
	}
	if result.State != "healthy" {
		result.Error = "节点出口观测失败"
	}
	result.CheckedAt = time.Now()
	result.DurationMS = time.Since(start).Milliseconds()
	return result
}

func ParseExitIP(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "ip=") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "ip="))
			if ip := net.ParseIP(value); ip != nil {
				return ip.String()
			}
		}
	}
	return ""
}

package speedtest

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

// CheckHealth authenticates and transfers a small HTTPS response through SOCKS5.
// It measures TCP forwarding only, not UDP or the local TUN path.
func CheckHealth(ctx context.Context, node model.SocksNode) error {
	return checkHealthTargets(ctx, node, []string{"https://www.cloudflare.com/cdn-cgi/trace", "https://www.google.com/generate_204"})
}

func checkHealthTargets(ctx context.Context, node model.SocksNode, targets []string) error {
	for _, target := range targets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(c context.Context, _, addr string) (net.Conn, error) {
			return dialSOCKS5(c, node.Addr, node.Username, node.Password, addr)
		}}
		client := &http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		req, _ := http.NewRequestWithContext(attempt, http.MethodGet, target, nil)
		resp, err := client.Do(req)
		if err == nil {
			_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				err = fmt.Errorf("unexpected status")
			}
		}
		tr.CloseIdleConnections()
		cancel()
		if err == nil {
			return nil
		}
	}
	// Do not put upstream credentials or request URLs into public status errors.
	return fmt.Errorf("SOCKS5 authentication or HTTPS forwarding failed for both health targets")
}

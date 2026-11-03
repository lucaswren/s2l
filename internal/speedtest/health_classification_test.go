package speedtest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPClassificationAndBodyFailure(t *testing.T) {
	for _, code := range []int{204, 403, 429, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
			defer target.Close()
			got := probeHTTPS(context.Background(), target.URL, func(c context.Context, addr string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(c, "tcp", addr)
			})
			want := "restricted"
			if code == 204 {
				want = "healthy"
			}
			if got.State != want || got.Stage != "http" || got.StatusCode != code {
				t.Fatalf("%+v", got)
			}
		})
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Write([]byte("short"))
	}))
	defer broken.Close()
	got := probeHTTPS(context.Background(), broken.URL, func(c context.Context, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(c, "tcp", addr)
	})
	if got.State != "unavailable" || got.Stage != "http" {
		t.Fatal(got)
	}
}
func TestTLSValidationAndCancellation(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	dial := func(c context.Context, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(c, "tcp", addr)
	}
	r := probeHTTPS(context.Background(), target.URL, dial)
	if r.State != "unavailable" || r.Stage != "tls" {
		t.Fatal(r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	r = probeHTTPS(ctx, "https://target.invalid", dial)
	if r.State == "healthy" || time.Since(start) > time.Second {
		t.Fatal(r)
	}
}
func TestSOCKSStagesPreserveWrappedErrors(t *testing.T) {
	for _, stage := range []string{"proxy-connect", "socks-auth", "target-connect"} {
		got := probeHTTPS(context.Background(), "https://target.invalid", func(context.Context, string) (net.Conn, error) {
			return nil, &StageError{Stage: stage, Err: fmt.Errorf("secret must never appear")}
		})
		if got.Stage != stage || got.State != "unavailable" || got.Error == "" || got.Error == "secret must never appear" {
			t.Fatal(got)
		}
	}
}

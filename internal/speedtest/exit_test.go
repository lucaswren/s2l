package speedtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func TestExitProofValidatesBodyAndUsesSOCKS(t *testing.T) {
	proxy := connectFixture(t)
	for _, body := range []string{"ip=203.0.113.4\n", "ip=2001:db8::4\n", "ip=not-an-ip\n", "html success page"} {
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		r := probeExitTarget(context.Background(), model.SocksNode{Addr: proxy}, target.URL)
		target.Close()
		expected := ParseExitIP(body)
		if (r.State == "healthy") != (expected != "") || r.ExitIP != expected {
			t.Fatal(body, r)
		}
	}
}

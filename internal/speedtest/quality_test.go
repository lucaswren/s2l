package speedtest

import (
	"context"
	"github.com/lucaswren/s2l/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestQualityDetectsTruncationAndBoundsLargeResponse(t *testing.T) {
	node := model.SocksNode{Addr: connectFixture(t)}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/broken":
			w.Header().Set("Content-Length", "100")
			w.Write([]byte("short"))
		case "/large":
			w.Write([]byte(strings.Repeat("x", 4096)))
		default:
			w.Write([]byte("ok"))
		}
	}))
	defer target.Close()
	broken := transferSample(context.Background(), node, target.URL+"/broken", 1024)
	if broken.Error == "" || broken.Complete {
		t.Fatal(broken)
	}
	large := transferSample(context.Background(), node, target.URL+"/large", 1024)
	if !large.Bounded || large.Complete || large.Received != 1025 {
		t.Fatal(large)
	}
	q := probeQuality(context.Background(), node, target.URL)
	if q.Successes != 8 || len(q.Samples) != 8 || q.P95MS < q.MedianMS {
		t.Fatal(q)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	q = probeQuality(ctx, node, target.URL)
	if len(q.Samples) != 0 || time.Since(started) > time.Second {
		t.Fatal(q)
	}
}

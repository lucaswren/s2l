package speedtest

import (
	"context"
	"github.com/lucaswren/s2l/internal/model"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthRequiresSOCKSForwardingAndTriesSecondTarget(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer target.Close()
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	go func() {
		for {
			conn, err := proxy.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				g := make([]byte, 3)
				if _, err := io.ReadFull(conn, g); err != nil {
					return
				}
				conn.Write([]byte{5, 0})
				hdr := make([]byte, 4)
				if _, err := io.ReadFull(conn, hdr); err != nil {
					return
				}
				switch hdr[3] {
				case 1:
					io.CopyN(io.Discard, conn, 6)
				case 3:
					var b [1]byte
					io.ReadFull(conn, b[:])
					io.CopyN(io.Discard, conn, int64(b[0])+2)
				default:
					return
				}
				up, err := net.Dial("tcp", target.Listener.Addr().String())
				if err != nil {
					return
				}
				defer up.Close()
				conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
				go io.Copy(up, conn)
				io.Copy(conn, up)
			}()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	node := model.SocksNode{Addr: proxy.Addr().String()}
	if err := checkHealthTargets(ctx, node, []string{target.URL + "/fail", target.URL + "/ok"}); err != nil {
		t.Fatal(err)
	}
	if err := checkHealthTargets(ctx, node, []string{target.URL + "/fail"}); err == nil {
		t.Fatal("HTTP failure reported healthy")
	}
	// A reachable TCP port without SOCKS forwarding cannot pass health checking.
	bad, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	go func() {
		c, e := bad.Accept()
		if e == nil {
			c.Write([]byte{5, 255})
			c.Close()
		}
	}()
	node.Addr = bad.Addr().String()
	if err := checkHealthTargets(ctx, node, []string{target.URL}); err == nil {
		t.Fatal("SOCKS rejection reported healthy")
	}
}

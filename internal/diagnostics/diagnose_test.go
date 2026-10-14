package diagnostics

import (
	"context"
	"encoding/binary"
	"github.com/lucaswren/s2l/internal/model"
	"io"
	"net"
	"testing"
)

func TestRejectUnsafeTargets(t *testing.T) {
	for _, target := range []string{"file:///etc/passwd", "https://user:pass@example.com/image", "https://example.com:8080/image"} {
		if _, err := ParseTarget(target); err == nil {
			t.Fatal(target)
		}
	}
	for _, target := range []string{"http://127.0.0.1/", "http://10.0.0.1/", "http://169.254.169.254/", "http://100.64.0.1/", "http://[::1]/"} {
		if _, err := Diagnose(context.Background(), model.SocksNode{}, target, nil); err == nil {
			t.Fatal("private target allowed", target)
		}
	}
}

func TestHTTPThroughSOCKSAndTruncatedBody(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "truncated"}[truncated], func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				c, err := listener.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				greeting := make([]byte, 3)
				if _, err := io.ReadFull(c, greeting); err != nil {
					return
				}
				c.Write([]byte{5, 0})
				request := make([]byte, 10)
				if _, err := io.ReadFull(c, request); err != nil {
					return
				}
				if request[3] != 1 || string(request[4:8]) != string([]byte{9, 9, 9, 9}) || binary.BigEndian.Uint16(request[8:]) != 80 {
					t.Error("diagnostic did not pin validated public destination")
				}
				c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				buf := make([]byte, 4096)
				c.Read(buf)
				body := "image"
				if truncated {
					body = "im"
				}
				io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Type: image/png\r\nContent-Length: 5\r\nConnection: close\r\n\r\n"+body)
			}()
			r, err := Diagnose(context.Background(), model.SocksNode{Addr: listener.Addr().String()}, "http://9.9.9.9/image?token=private", nil)
			if err != nil {
				t.Fatal(err)
			}
			<-done
			if r.SOCKS.State != "healthy" || r.HTTP.State != "healthy" || r.StatusCode != 200 {
				t.Fatal(r)
			}
			if (r.Download.State == "failed") != truncated {
				t.Fatal(r.Download)
			}
			if r.Target != "http://9.9.9.9/image" {
				t.Fatal("query token exposed", r.Target)
			}
		})
	}
}

package diagnostics

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func forwardSOCKSFixture(t *testing.T, target string) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		client, err := listener.Accept()
		if err != nil {
			return
		}
		defer client.Close()
		client.SetDeadline(time.Now().Add(5 * time.Second))
		g := make([]byte, 3)
		if _, err := io.ReadFull(client, g); err != nil {
			return
		}
		client.Write([]byte{5, 0})
		request := make([]byte, 10)
		if _, err := io.ReadFull(client, request); err != nil {
			return
		}
		if request[0] != 5 || request[1] != 1 || request[3] != 1 {
			return
		}
		remote, err := net.DialTimeout("tcp", target, time.Second)
		if err != nil {
			return
		}
		defer remote.Close()
		remote.SetDeadline(time.Now().Add(5 * time.Second))
		client.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		done := make(chan struct{})
		go func() { io.Copy(remote, client); remote.Close(); close(done) }()
		io.Copy(client, remote)
		client.Close()
		<-done
	}()
	return listener.Addr().String()
}

func TestHTTPSDiagnosticStagesAndCertificateValidation(t *testing.T) {
	for _, trust := range []bool{true, false} {
		name := "untrusted-certificate"
		if trust {
			name = "verified-HTTPS"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("diagnostic leaked application credentials")
				}
				w.Header().Set("Content-Type", "image/png")
				w.Write([]byte("image"))
			}))
			defer server.Close()
			cert, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
			if err != nil {
				t.Fatal(err)
			}
			cfg := &tls.Config{MinVersion: tls.VersionTLS12}
			if len(cert.DNSNames) > 0 {
				cfg.ServerName = cert.DNSNames[0]
			} else {
				cfg.ServerName = cert.IPAddresses[0].String()
			}
			if trust {
				cfg.RootCAs = server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
			}
			proxy := forwardSOCKSFixture(t, server.Listener.Addr().String())
			r, err := diagnose(context.Background(), model.SocksNode{Addr: proxy}, "https://9.9.9.9/image", nil, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if r.SOCKS.State != "healthy" {
				t.Fatal(r.SOCKS)
			}
			if trust {
				if r.TLS.State != "healthy" || r.HTTP.State != "healthy" || r.Download.State != "healthy" || r.Bytes != 5 {
					t.Fatal(r)
				}
			} else if r.TLS.State != "failed" || r.HTTP.State != "skipped" || r.Download.State != "skipped" {
				t.Fatal("certificate failure was not attributed to TLS", r)
			}
		})
	}
}

func TestDiagnosticBodyLimitAndRedirectHandling(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		name := "bounded-body"
		if redirect {
			name = "redirect-not-followed"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if redirect {
					w.Header().Set("Location", "http://127.0.0.1:1/private")
					w.WriteHeader(302)
					return
				}
				w.Write(make([]byte, 3*1024*1024))
			}))
			defer server.Close()
			proxy := forwardSOCKSFixture(t, server.Listener.Addr().String())
			r, err := Diagnose(context.Background(), model.SocksNode{Addr: proxy}, "http://9.9.9.9/image", nil)
			if err != nil {
				t.Fatal(err)
			}
			if redirect {
				if r.StatusCode != 302 || r.HTTP.State != "failed" {
					t.Fatal(r)
				}
			} else if !r.Limited || r.Bytes != 2*1024*1024+1 || r.Download.State != "limited" {
				t.Fatal("download exceeded diagnostic bounds", r)
			}
		})
	}
}

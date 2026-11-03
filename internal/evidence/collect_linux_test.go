//go:build linux

package evidence

import (
	"bytes"
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

// Runs only in the isolated Linux VM/CI namespace, never against a live server.
func TestLiveCapturePreservesHeadersWithoutApplicationPayload(t *testing.T) {
	if os.Getenv("S2L_EVIDENCE_TEST") != "1" {
		t.Skip("set S2L_EVIDENCE_TEST=1 in an isolated Linux environment")
	}
	if !Available() {
		t.Fatal("tcpdump required for live capture verification")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan Collected, 1)
	go func() {
		done <- CollectSystem(ctx, Request{Mapping: model.TunnelMapping{Subnet: "127.0.0.0/8"}, Node: model.SocksNode{Addr: listener.Addr().String()}})
	}()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("private-application-payload"))
			c.Close()
		}
	}()
	for i := 0; i < 20; i++ {
		c, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = c.Write([]byte("private-authentication-secret"))
		c.Close()
		time.Sleep(100 * time.Millisecond)
	}
	result := <-done
	if len(result.PCAP) <= 24 {
		t.Fatal("no captured headers", result.Capture)
	}
	if bytes.Contains(result.PCAP, []byte("private")) {
		t.Fatal("raw application payload retained")
	}
	if result.Snapshots["rules"] == "" || result.Snapshots["routes"] == "" {
		t.Fatal("kernel snapshots absent")
	}
}

func TestCommandCancellationDoesNotWaitForDescendantPipes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := commandText(ctx, []string{"sh", "-c", "sleep 10 & wait"})
	if time.Since(started) > 3*time.Second || result == "" {
		t.Fatal("descendant held output after command cancellation", time.Since(started), result)
	}
}

func TestNodeCaptureDoesNotDependOnResolvingDomainNodes(t *testing.T) {
	for _, tc := range []struct{ addr, want string }{
		{" proxy.example.invalid:1080 ", "(tcp port 1080)"},
		{"192.0.2.1:1080", "(tcp and host 192.0.2.1)"},
		{"[2001:db8::1]:1080", ""},
		{"proxy.example.invalid:1080 or ip", ""},
		{"proxy.example.invalid:65536", ""},
	} {
		if got := nodeCaptureTerm(tc.addr); got != tc.want {
			t.Fatal(tc.addr, got, tc.want)
		}
	}
}

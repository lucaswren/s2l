package speedtest

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestAuthenticationRejectionIsDistinctFromNegotiationFailure(t *testing.T) {
	for _, reply := range [][]byte{{1, 1}, {1, 7}, {2, 1}} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			c, err := listener.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(time.Second))
			greeting := make([]byte, 3)
			io.ReadFull(c, greeting)
			c.Write([]byte{5, 2})
			auth := make([]byte, 5)
			io.ReadFull(c, auth)
			c.Write(reply)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err = DialSOCKS5(ctx, listener.Addr().String(), "u", "p", "example.com:443")
		cancel()
		listener.Close()
		<-done
		if reply[0] == 1 {
			if ErrorStage(err) != "socks-auth-rejected" || AuthReplyCode(err) != reply[1] {
				t.Fatal(err)
			}
		} else if ErrorStage(err) != "socks-auth" || AuthReplyCode(err) != 0 {
			t.Fatal(err)
		}
	}
}

func TestHTTPSReportPreservesAuthenticationRejection(t *testing.T) {
	result := probeHTTPS(context.Background(), "https://example.com/", func(context.Context, string) (net.Conn, error) {
		return nil, &StageError{Stage: "socks-auth-rejected", Err: &authReplyError{code: 1}}
	})
	if result.Stage != "socks-auth-rejected" || result.AuthReply != 1 || result.SOCKSReply != 0 || result.Error != "上游拒绝 SOCKS5 账号密码" {
		t.Fatalf("%+v", result)
	}
	timeout := probeHTTPS(context.Background(), "https://example.com/", func(context.Context, string) (net.Conn, error) {
		return nil, &StageError{Stage: "socks-auth", Err: context.DeadlineExceeded}
	})
	if timeout.Stage != "socks-auth" || timeout.AuthReply != 0 {
		t.Fatalf("%+v", timeout)
	}
}

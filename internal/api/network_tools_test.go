package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/debuglog"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
)

func TestProbeTargetValidationAndOutputBound(t *testing.T) {
	for _, target := range []string{"example.com:443", "127.0.0.1:80", "[::1]:443"} {
		if !validProbeTarget(target) {
			t.Fatal(target)
		}
	}
	for _, target := range []string{"--help:80", "bad host:80", "a:0", "a:65536", "a:http", "a:80;id", "a\n:80"} {
		if validProbeTarget(target) {
			t.Fatal(target)
		}
	}
	b := &boundedOutput{}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := bytes.Repeat([]byte("a"), 50000)
			n, err := b.Write(p)
			if n != len(p) || err != nil {
				t.Error(n, err)
			}
		}()
	}
	wg.Wait()
	if len(b.data) != 32768 || b.total != 4*50000 {
		t.Fatal(len(b.data))
	}
}

func TestNetworkTCPingUsesProxyAndDoesNotRecordWithDebugOff(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	failures := make(chan error, 1)
	go func() {
		for i := 0; i < 3; i++ {
			conn, err := listener.Accept()
			if err != nil {
				failures <- err
				return
			}
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			err = func() error {
				defer conn.Close()
				greeting := make([]byte, 3)
				if _, err := io.ReadFull(conn, greeting); err != nil {
					return err
				}
				if !bytes.Equal(greeting, []byte{5, 1, 0}) {
					return io.ErrUnexpectedEOF
				}
				if _, err := conn.Write([]byte{5, 0}); err != nil {
					return err
				}
				header := make([]byte, 5)
				if _, err := io.ReadFull(conn, header); err != nil {
					return err
				}
				if header[0] != 5 || header[1] != 1 || header[3] != 3 {
					return io.ErrUnexpectedEOF
				}
				target := make([]byte, int(header[4])+2)
				if _, err := io.ReadFull(conn, target); err != nil {
					return err
				}
				if string(target[:len(target)-2]) != "target.invalid" || !bytes.Equal(target[len(target)-2:], []byte{1, 187}) {
					return io.ErrUnexpectedEOF
				}
				_, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80})
				return err
			}()
			if err != nil {
				failures <- err
				return
			}
		}
		failures <- nil
	}()
	st, err := store.New(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNode(model.SocksNode{ID: "n", Name: "node", Addr: listener.Addr().String()}); err != nil {
		t.Fatal(err)
	}
	s := NewServer(st, nil, nil)
	debuglog.Default.Set(false)
	before := debuglog.Default.Capture()
	r := httptest.NewRequest(http.MethodPost, "/api/nodes/n/network-test", strings.NewReader(`{"kind":"tcping","target":"target.invalid:443"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		OK        bool `json:"ok"`
		Successes int  `json:"successes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || !result.OK || result.Successes != 3 {
		t.Fatal(w.Body.String(), err)
	}
	if err := <-failures; err != nil {
		t.Fatal(err)
	}
	after := debuglog.Default.Capture()
	if !bytes.Equal(before.Logs, after.Logs) {
		t.Fatal("recorded logs while debug off")
	}
	s.networkTests <- struct{}{}
	s.networkTests <- struct{}{}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/api/nodes/n/network-test", strings.NewReader(`{"kind":"tcping","target":"target.invalid:443"}`))
	s.handleNodeNetworkTest(w, r, "n")
	if w.Code != 429 {
		t.Fatal(w.Code)
	}
}

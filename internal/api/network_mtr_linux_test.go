//go:build linux

package api

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMTRHandlerStreamsBeforeExitAndCancelsProcess(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pid")
	script := "#!/bin/sh\nprintf '%s' $$ > \"$MTR_TEST_PID\"\nprintf 'h 0 127.0.0.1\\np 0 2000 1\\n'\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "mtr"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MTR_TEST_PID", pidPath)
	st, err := store.New(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNode(model.SocksNode{ID: "n", Addr: "127.0.0.1:1080"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(st, nil, nil).Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/nodes/n/network-test", strings.NewReader(`{"kind":"mtr","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() || !strings.Contains(scanner.Text(), `"type":"start"`) {
		t.Fatal("no initial stream frame", scanner.Err())
	}
	found := false
	for scanner.Scan() {
		var event struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "output" {
			found = true
			break
		}
		if event.Type == "result" {
			t.Fatal("command finished before live output", string(event.Data))
		}
	}
	if !found {
		t.Fatal("no live output", scanner.Err())
	}
	pidBytes, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	response.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("MTR process survived request cancellation")
}

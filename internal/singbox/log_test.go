package singbox

import (
	"errors"
	"github.com/lucaswren/s2l/internal/debuglog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogRotationBoundedAndRestartPreservesEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process.log")
	l, err := openRotatingLog(path)
	if err != nil {
		t.Fatal(err)
	}
	l.limit = 64
	for i := 0; i < 8; i++ {
		if _, err := l.Write([]byte("ERROR tls failed connection refused timeout\n")); err != nil {
			t.Fatal(err)
		}
	}
	s := l.Status()
	if s.Timeouts != 8 || s.Refused != 8 || s.TLSFailures != 8 {
		t.Fatal(s)
	}
	l.Close()
	for _, name := range []string{path, path + ".1", path + ".2", path + ".3"} {
		info, err := os.Stat(name)
		if err != nil || info.Size() > 64 {
			t.Fatalf("unbounded or missing backup %s: %v", name, err)
		}
	}
	if _, err := os.Stat(path + ".4"); !os.IsNotExist(err) {
		t.Fatal("extra backup")
	}
	old, _ := os.ReadFile(path)
	again, err := openRotatingLog(path)
	if err != nil {
		t.Fatal(err)
	}
	again.Write([]byte("new process\n"))
	again.Close()
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), string(old)) {
		t.Fatal("restart truncated prior evidence")
	}
}

func TestDisabledWriterCountsFragmentedLinesAndRejectsWritesAfterClose(t *testing.T) {
	debuglog.Default.Set(false)
	before := string(debuglog.Default.Snapshot())
	w, _ := openDebugLog(filepath.Join(t.TempDir(), "no-log"))
	line := "TLS ERROR: TIMEOUT connection refused network is unreachable deadline exceeded\n"
	for _, ch := range []byte(line) {
		if _, err := w.Write([]byte{ch}); err != nil {
			t.Fatal(err)
		}
	}
	s := w.Status()
	if s.Timeouts != 1 || s.TLSFailures != 1 || s.Refused != 1 || s.NetworkErrors != 1 {
		t.Fatal("chunk boundaries lost classification", s)
	}
	if string(debuglog.Default.Snapshot()) != before {
		t.Fatal("disabled stream retained troubleshooting logs")
	}
	w.Close()
	if _, err := w.Write([]byte("after close\n")); !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed log still accepted writes", err)
	}
}

func TestDebugWriterDoesNotCreateFilesOrCollectWhenDisabled(t *testing.T) {
	debuglog.Default.Set(false)
	path := filepath.Join(t.TempDir(), "unused.log")
	writer, err := openDebugLog(path)
	if err != nil {
		t.Fatal(err)
	}
	before := string(debuglog.Default.Snapshot())
	writer.Write([]byte("timeout\n"))
	if string(debuglog.Default.Snapshot()) != before {
		t.Fatal("collected while disabled")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created disk log")
	}
	if writer.Status().Timeouts != 1 {
		t.Fatal("health counters stopped")
	}
	debuglog.Default.Set(true)
	defer debuglog.Default.Set(false)
	writer.Write([]byte("debug evidence\n"))
	if !strings.Contains(string(debuglog.Default.Snapshot()), "debug evidence") {
		t.Fatal("missing enabled log")
	}
	debuglog.Default.Set(false)
	before = string(debuglog.Default.Snapshot())
	writer.Write([]byte("after stop\n"))
	if string(debuglog.Default.Snapshot()) != before {
		t.Fatal("collected after stop")
	}
}

func TestProxyRejectionsAcrossChunks(t *testing.T) {
	writer, err := openDebugLog("unused")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, fragment := range []string{"socks5: request re", "jected, code=9\n", "socks5: request rejected, code=2\n"} {
		if _, err := writer.Write([]byte(fragment)); err != nil {
			t.Fatal(err)
		}
	}
	if writer.Status().ProxyRejections != 2 {
		t.Fatal(writer.Status())
	}
}

package singbox

import (
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

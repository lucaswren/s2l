//go:build linux

package network

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTuneInterfaceReportsRequiredFailures(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *"$S2L_TUNE_FAIL"*) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "ip"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(dir, "sysctl"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"mtu", "up", "txqueuelen"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("S2L_TUNE_FAIL", failure)
			err := TuneInterface("tun101", 1400)
			if err == nil {
				t.Fatalf("failure=%s, error=%v", failure, err)
			}
		})
	}
}

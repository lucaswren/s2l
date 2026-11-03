//go:build linux

package l2tp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActiveDaemonAccountUpdatesNeverRestart(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "commands")
	script := `#!/bin/sh
echo "$*" >> "$S2L_DAEMON_LOG"
if [ "$1" = "is-active" ]; then echo active; exit 0; fi
exit 99
`
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("S2L_DAEMON_LOG", log)
	if err := EnsureDaemon(false); err != nil {
		t.Fatal(err)
	}
	if AllowStructuralChange() == nil {
		t.Fatal("active daemon permitted structural changes")
	}
	data, _ := os.ReadFile(log)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "is-active xl2tpd" {
			t.Fatalf("account update changed daemon lifecycle: %s", data)
		}
	}
}

func TestUnknownDaemonStateFailsClosed(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\necho unable-to-connect\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := daemonActive(context.Background()); err == nil {
		t.Fatal("unknown state treated as stopped")
	}
	if AllowStructuralChange() == nil {
		t.Fatal("unknown state permitted config overwrite")
	}
}

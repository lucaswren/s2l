//go:build linux || darwin

package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLISettingsReadsAfterWebTransaction(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required for the CLI interoperability test")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	initial := []byte(`{"admin_user":"old","admin_pass":"fixture-password","ui_language":"en"}`)
	if err := os.WriteFile(path, initial, 0600); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockSettingsFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-c", `
import sys
from pathlib import Path
sys.path.insert(0, "../../script")
import manage_config as config
config.CONFIG_FILE = Path(sys.argv[1])
config.restart_service = lambda: None
sys.argv = ["manage_config.py", "admin_user"]
config.main()
`, path)
	cmd.Stdin = strings.NewReader("updated")
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("CLI finished while Web held the configuration lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	blocked, cancelBlocked := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancelBlocked()
	if release, err := lockSettingsFile(blocked, path); !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatalf("another web request acquired the same file lock: %v", err)
	}
	if err := writeSettings(path, []byte(strings.ReplaceAll(string(initial), `"en"`, `"zh-CN"`))); err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]string
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config["ui_language"] != "zh-CN" || config["admin_user"] != "updated" {
		t.Fatalf("one settings update was lost: %+v", config)
	}
}

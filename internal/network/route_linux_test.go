//go:build linux

package network

import (
	"github.com/lucaswren/s2l/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanupRejectsReservedTables(t *testing.T) {
	for _, id := range []int{253, 254, 255} {
		m := model.TunnelMapping{TunName: "tun101", TableID: id, Subnet: "10.0.101.0/24"}
		if err := DelPolicyRoute(m); err == nil {
			t.Fatalf("cleanup accepted reserved table %d", id)
		}
	}
}

func TestPolicyRouteFailureRollsBackAllChanges(t *testing.T) {
	for _, failure := range []string{"rule add", "route add default"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "commands")
			// Fake ip records commands without touching the host network.
			script := `#!/bin/sh
echo "$*" >> "$S2L_IP_LOG"
case "$*" in
  "$S2L_IP_FAIL"*) echo "injected failure" >&2; exit 1 ;;
  "rule del"*) echo "No such file or directory" >&2; exit 1 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "ip"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("S2L_IP_LOG", log)
			t.Setenv("S2L_IP_FAIL", failure)
			m := model.TunnelMapping{TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24"}
			if err := AddPolicyRoute(m); err == nil {
				t.Fatal("expected injected route failure")
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			commands := string(data)
			if strings.Count(commands, "route flush table 101\n") != 2 || !strings.HasSuffix(commands, "rule del from 10.0.101.0/24 table 101\n") {
				t.Fatalf("partial routes survived failure:\n%s", commands)
			}
		})
	}
}

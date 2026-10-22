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
  "-N -j rule show") if [ -f "$S2L_IP_STATE/rule" ]; then echo '[{"src":"10.0.101.0/24","table":101,"priority":10101,"protocol":196}]'; else echo '[]'; fi ;;
  "-j route show table 101") if [ -f "$S2L_IP_STATE/guard" ]; then echo '[{"dst":"default","type":"unreachable","metric":32767,"protocol":196}]'; else echo '[]'; fi ;;
  "route add unreachable"*) touch "$S2L_IP_STATE/guard" ;;
  "rule add"*) touch "$S2L_IP_STATE/rule" ;;
  "route del unreachable"*) rm -f "$S2L_IP_STATE/guard" ;;
  "rule del"*) if [ -f "$S2L_IP_STATE/rule" ]; then rm "$S2L_IP_STATE/rule"; else echo "No such file or directory" >&2; exit 1; fi ;;
esac
`
			t.Setenv("S2L_IP_STATE", dir)
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
			if strings.Contains(commands, "route flush") {
				t.Fatal("failure rollback flushed a routing table", commands)
			}
			if _, err := os.Stat(filepath.Join(dir, "guard")); !os.IsNotExist(err) {
				t.Fatal("partial fallback route survived failure", commands)
			}
			if _, err := os.Stat(filepath.Join(dir, "rule")); !os.IsNotExist(err) {
				t.Fatal("partial source rule survived failure", commands)
			}
			if !strings.Contains(commands, failure) {
				t.Fatal("did not exercise requested failure", commands)
			}
		})
	}
}

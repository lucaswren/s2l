//go:build linux

package network

import (
	"github.com/lucaswren/s2l/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMappingTCPMSSIsScopedAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "commands")
	script := `#!/bin/sh
echo "$*" >> "$S2L_MSS_LOG"
shift 4
operation="$1"; shift
chain="$1"; shift
if [ "$operation" = "-I" ]; then shift; fi
key=$(printf '%s' "$chain $*" | cksum | cut -d ' ' -f 1)
case "$operation" in
 -C) test -f "$S2L_MSS_STATE/$key" ;;
 -I) touch "$S2L_MSS_STATE/$key" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "iptables"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("S2L_MSS_LOG", logPath)
	t.Setenv("S2L_MSS_STATE", dir)
	mappings := []model.TunnelMapping{{ID: "a", Subnet: "10.0.101.0/24", MSS: 1100}, {ID: "b", Subnet: "10.0.102.0/24", MSS: 1200}}
	for i := 0; i < 2; i++ {
		for _, m := range mappings {
			if err := addMappingTCPMSS(m); err != nil {
				t.Fatal(err)
			}
		}
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(data)
	if strings.Count(commands, "-t mangle -I FORWARD 1") != 4 {
		t.Fatalf("MSS rules missing or duplicated: %s", commands)
	}
	for _, m := range mappings {
		for _, direction := range []string{"-i", "-o"} {
			source := "-s"
			if direction == "-o" {
				source = "-d"
			}
			if strings.Count(commands, "-I FORWARD 1 "+direction+" ppp+ "+source+" "+m.Subnet) != 1 {
				t.Fatalf("rule escaped mapping scope: %s", commands)
			}
		}
	}
	if strings.Contains(commands, " -D ") || strings.Contains(commands, "-t filter") {
		t.Fatalf("setup removed existing protection: %s", commands)
	}
	if !strings.Contains(commands, "--set-mss 1100") || !strings.Contains(commands, "--set-mss 1200") {
		t.Fatal("mapping-specific MSS ignored")
	}
}

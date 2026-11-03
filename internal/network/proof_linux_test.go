//go:build linux

package network

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func TestProofSourceNeverUsesAssignedPeer(t *testing.T) {
	for _, subnet := range []string{"10.0.101.0/24", "10.0.101.0/29", "10.0.100.0/23"} {
		ip, err := proofSource(subnet)
		if err != nil || strings.HasSuffix(ip, ".2") {
			t.Fatal(ip, err)
		}
	}
	if _, err := proofSource("10.0.0.0/30"); err == nil {
		t.Fatal("no spare address accepted")
	}
}

func TestForwardingDiagnosticUsesGuardAndAlwaysCleansUp(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$*" >> "$S2L_PROOF_LOG"
case "$*" in
 '-j route show exact '*) echo '[]' ;;
 *'curl '*)
  if [ "$S2L_PROOF_FAIL" = 1 ]; then exit 7; fi
  echo 'ip=203.0.113.5' ;;
esac
`
	for _, name := range []string{"ip", "curl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("S2L_PROOF_LOG", log)
	m := model.TunnelMapping{ID: "m", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24"}
	for _, failure := range []string{"0", "1"} {
		t.Setenv("S2L_PROOF_FAIL", failure)
		os.WriteFile(log, nil, 0600)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		r := ProbeForwarding(ctx, m)
		cancel()
		if (r.State == "healthy") != (failure == "0") {
			t.Fatal(r)
		}
		data, _ := os.ReadFile(log)
		calls := string(data)
		if !strings.Contains(calls, "link add pppst") || !strings.Contains(calls, "netns del s2l-proof-") || !strings.Contains(calls, "link del pppst") {
			t.Fatal(calls)
		}
		if strings.Contains(calls, "iptables") || strings.Contains(calls, "rule add") {
			t.Fatal("diagnostic changed permanent rules", calls)
		}
	}
}

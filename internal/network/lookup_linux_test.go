//go:build linux

package network

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func TestPeerRouteLookupDetectsBothDirectionsAndNeverMutates(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$*" >> "$S2L_LOOKUP_LOG"
case "$*" in
 '-4 -N -j route get 10.0.101.2 from 1.1.1.1 iif tun101') printf '[{"dev":"%s"}]\n' "$S2L_RETURN_DEV";;
 '-4 -N -j route get '*) printf '[{"dev":"tun101","table":%s}]\n' "$S2L_OUT_TABLE";;
 *) exit 99;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "ip"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("S2L_LOOKUP_LOG", log)
	m := model.TunnelMapping{ID: "m", Subnet: "10.0.101.0/24", TunName: "tun101", TableID: 101}
	peers := []model.PPPSession{{MappingID: "m", Interface: "ppp0", PeerIP: "10.0.101.2"}}
	for _, tc := range []struct {
		table, dev string
		ok         bool
	}{{"101", "ppp0", true}, {"102", "ppp0", false}, {"101", "ppp2", false}} {
		t.Setenv("S2L_OUT_TABLE", tc.table)
		t.Setenv("S2L_RETURN_DEV", tc.dev)
		err := VerifyPeerRoutes(context.Background(), m, peers)
		if (err == nil) != tc.ok || IsMissingState(err) {
			t.Fatal(tc, err)
		}
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if !strings.HasPrefix(line, "-4 -N -j route get ") {
			t.Fatal("mutating lookup", line)
		}
	}
}

func TestEffectiveForwardedRouteIntegration(t *testing.T) {
	if os.Getenv("S2L_NETNS_TEST") != "1" {
		t.Skip("requires isolated root network namespace")
	}
	must := func(name string, args ...string) {
		t.Helper()
		if err := run(name, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"ppp73", "ppp74", "tun171", "tun172"} {
		must("ip", "link", "add", name, "type", "dummy")
		defer run("ip", "link", "del", name)
		must("ip", "link", "set", name, "up")
	}
	must("ip", "addr", "add", "10.0.171.1/24", "dev", "ppp73")
	must("ip", "addr", "add", "10.0.172.1/24", "dev", "ppp74")
	must("sysctl", "-w", "net.ipv4.ip_forward=1")
	must("sysctl", "-w", "net.ipv4.conf.all.rp_filter=0")
	m := model.TunnelMapping{ID: "sg", Subnet: "10.0.171.0/24", TunName: "tun171", TableID: 171}
	u := model.TunnelMapping{ID: "us", Subnet: "10.0.172.0/24", TunName: "tun172", TableID: 172}
	for _, mapping := range []model.TunnelMapping{m, u} {
		if err := AddPolicyRoute(mapping); err != nil {
			t.Fatal(err)
		}
		defer DelPolicyRoute(mapping)
	}
	peers := []model.PPPSession{{MappingID: "sg", Interface: "ppp73", PeerIP: "10.0.171.2"}, {MappingID: "us", Interface: "ppp74", PeerIP: "10.0.172.2"}}
	check := func(mapping model.TunnelMapping, ok bool) {
		t.Helper()
		err := VerifyPeerRoutes(context.Background(), mapping, peers)
		if (err == nil) != ok || IsMissingState(err) {
			t.Fatal(mapping.ID, ok, err)
		}
	}
	// A normal IPsec-style table only routes outer peers and must not be
	// mistaken for a conflicting default policy merely due to its priority.
	must("ip", "rule", "add", "priority", "220", "table", "220")
	defer run("ip", "rule", "del", "priority", "220", "table", "220")
	must("ip", "route", "add", "198.18.0.2/32", "dev", "tun172", "table", "220")
	check(m, true)
	check(u, true)
	must("ip", "rule", "add", "priority", "1000", "from", m.Subnet, "table", "172")
	check(m, false)
	check(u, true)
	must("ip", "rule", "del", "priority", "1000", "from", m.Subnet, "table", "172")
	check(m, true)
	must("ip", "route", "add", "10.0.171.2/32", "dev", "ppp74")
	check(m, false)
	check(u, true)
	must("ip", "route", "del", "10.0.171.2/32", "dev", "ppp74")
	check(m, true)
}

//go:build linux

package network

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func TestVerificationRoundReusesOnlyCompleteUnexpiredSnapshots(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "commands")
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("S2L_TEST_COMMAND_LOG", log)
	for _, name := range []string{"ip", "iptables", "ip6tables"} {
		script := "#!/bin/sh\nprintf '%s\\n' \"" + name + "\" >> \"$S2L_TEST_COMMAND_LOG\"\n"
		if name == "ip6tables" {
			script += "test \"$S2L_TEST_FAIL_READ\" != 1\n"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	var round VerificationRound
	for range 2 {
		if err := round.refresh(ctx, 0); err != nil {
			t.Fatal(err)
		}
	}
	round.at = time.Now().Add(-time.Second)
	t.Setenv("S2L_TEST_FAIL_READ", "1")
	if err := round.refresh(ctx, 0); err == nil {
		t.Fatal("incomplete snapshot was accepted")
	}
	t.Setenv("S2L_TEST_FAIL_READ", "")
	if err := round.refresh(ctx, 0); err != nil {
		t.Fatal(err)
	}
	// A managed mutation makes even a newly captured snapshot obsolete.
	if err := round.refresh(ctx, 1); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil || strings.Count(string(data), "\n") != 12 {
		t.Fatalf("expected three commands for each fresh read, got %q (%v)", data, err)
	}
}

func TestVerifyRouteJSONRejectsMissingRulesAndUnderstandsSystemTables(t *testing.T) {
	m := model.TunnelMapping{Subnet: "10.0.101.2/24", TableID: 101, TunName: "tun101"}
	rules := []byte(`[{"src":"all","table":"local","priority":0},{"src":"10.0.101.0","srclen":24,"table":"101","priority":10101,"protocol":196},{"src":"all","table":"main","priority":32766}]`)
	routes := []byte(`[{"dst":"default","dev":"tun101","metric":100,"protocol":196},{"dst":"default","type":"unreachable","metric":32767,"protocol":196}]`)
	if err := verifyRouteJSON(m, rules, routes); err != nil {
		t.Fatal(err)
	}
	numericRoutes := []byte(`[{"dst":"default","dev":"tun101","type":"1","metric":100,"protocol":"196","scope":"253","flags":[]},{"dst":"default","type":"7","metric":32767,"protocol":"196","flags":[]}]`)
	if err := verifyRouteJSON(m, rules, numericRoutes); err != nil {
		t.Fatal("ip -N numeric route types rejected", err)
	}
	if verifyRouteJSON(m, []byte(`[]`), routes) == nil || verifyRouteJSON(m, rules, []byte(`[]`)) == nil {
		t.Fatal("missing forwarding path accepted")
	}
}

func TestRouteConflictIsReportedWithoutAuthorizingDestructiveRepair(t *testing.T) {
	m := model.TunnelMapping{Subnet: "10.0.101.0/24", TableID: 101, TunName: "tun101"}
	baseRule := map[string]any{"src": m.Subnet, "table": 101, "priority": 10101, "protocol": 196}
	baseRoute := map[string]any{"dst": "default", "dev": "tun101", "metric": 100, "protocol": 196}
	guard := map[string]any{"dst": "default", "type": "unreachable", "metric": 32767, "protocol": 196}
	for _, tc := range []struct {
		name        string
		rule, route map[string]any
		extra       map[string]any
	}{
		{"more specific foreign route", baseRule, baseRoute, map[string]any{"dst": "1.1.1.1/32", "dev": "tun102", "protocol": "static"}},
		{"gateway override", baseRule, map[string]any{"dst": "default", "dev": "tun101", "metric": 100, "protocol": 196, "gateway": "172.19.101.2"}, nil},
		{"conditional rule", map[string]any{"src": m.Subnet, "table": 101, "priority": 10101, "protocol": 196, "fwmark": 1}, baseRoute, nil},
		{"other table at same priority", map[string]any{"src": m.Subnet, "table": 102, "priority": 10101, "protocol": 196}, baseRoute, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules, _ := json.Marshal([]any{tc.rule})
			routes := []any{tc.route, guard}
			if tc.extra != nil {
				routes = append(routes, tc.extra)
			}
			b, _ := json.Marshal(routes)
			err := verifyRouteJSON(m, rules, b)
			if err == nil || IsMissingState(err) {
				t.Fatal("foreign route accepted or scheduled for automatic overwrite", err)
			}
		})
	}
}

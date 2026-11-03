//go:build linux

package network

import (
	"encoding/json"
	"github.com/lucaswren/s2l/internal/model"
	"testing"
)

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

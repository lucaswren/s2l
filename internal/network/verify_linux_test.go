//go:build linux

package network

import (
	"github.com/lucaswren/s2l/internal/model"
	"testing"
)

func TestVerifyRouteJSONRejectsMissingRulesAndUnderstandsSystemTables(t *testing.T) {
	m := model.TunnelMapping{Subnet: "10.0.101.2/24", TableID: 101, TunName: "tun101"}
	rules := []byte(`[{"src":"all","table":"local","priority":0},{"src":"10.0.101.0","srclen":24,"table":"101","priority":10101},{"src":"all","table":"main","priority":32766}]`)
	routes := []byte(`[{"dst":"default","dev":"tun101","metric":100},{"dst":"default","type":"unreachable","metric":32767}]`)
	if err := verifyRouteJSON(m, rules, routes); err != nil {
		t.Fatal(err)
	}
	if verifyRouteJSON(m, []byte(`[]`), routes) == nil || verifyRouteJSON(m, rules, []byte(`[]`)) == nil {
		t.Fatal("missing forwarding path accepted")
	}
}

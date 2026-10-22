//go:build linux

package network

import (
	"github.com/lucaswren/s2l/internal/model"
	"testing"
)

func TestForeignRoutingResourcesAreNotOwned(t *testing.T) {
	m := model.TunnelMapping{Subnet: "10.0.101.0/24", TableID: 101, TunName: "tun101"}
	goodRules := []resourceRule{{Src: m.Subnet, Protocol: 196, Table: float64(101), Priority: 10101}}
	goodRoutes := []resourceRoute{{Dst: "default", Protocol: 196, Dev: "tun101", Metric: 100}, {Dst: "default", Protocol: 196, Type: "unreachable", Metric: 32767}}
	if e := validateRouteOwnership(m, goodRules, goodRoutes); e != nil {
		t.Fatal(e)
	}
	for _, routes := range [][]resourceRoute{{{Dst: "10.3.0.0/16", Dev: "eth0"}}, {{Dst: "default", Protocol: 196, Dev: "tun101", Metric: 100, Gateway: "192.0.2.1"}}} {
		if validateRouteOwnership(m, goodRules, routes) == nil {
			t.Fatal("foreign route accepted", routes)
		}
	}
	for _, r := range []resourceRule{{Src: "all", Table: 101, Priority: 10}, {Src: m.Subnet, Table: 101, Priority: 10101, IIF: "eth0"}, {Src: m.Subnet, Table: 102, Priority: 10101}} {
		if validateRouteOwnership(m, []resourceRule{r}, goodRoutes) == nil {
			t.Fatal("foreign rule accepted", r)
		}
	}
}

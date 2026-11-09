package api

import (
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/validation"
	"testing"
)

func TestRejectReservedRoutingTables(t *testing.T) {
	m := model.TunnelMapping{NodeID: "node", TunName: "tun101", TableID: 252, Subnet: "10.0.101.0/24", L2TPUser: "user", L2TPPass: "password"}
	if err := validateMapping(m); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{253, 254, 255} {
		m.TableID = id
		if err := validateMapping(m); err == nil {
			t.Fatalf("reserved table %d accepted", id)
		}
	}
}

func TestRejectMappingMarkerInjection(t *testing.T) {
	m := model.TunnelMapping{ID: "map_1\n\"attacker\" * \"password\" *", NodeID: "node", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "user", L2TPPass: "password"}
	if err := validateMapping(m); err == nil {
		t.Fatal("mapping id with injected CHAP line accepted")
	}
}

func TestLegacyPPPNameIsReadableAndCanBeRenamed(t *testing.T) {
	node := model.SocksNode{ID: "n", Name: "node", Addr: "127.0.0.1:1080"}
	mapping := model.TunnelMapping{ID: "m", NodeID: node.ID, TunName: "ppp900", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "user", L2TPPass: "password"}
	if err := validation.State(model.AppState{Nodes: []model.SocksNode{node}, Mappings: []model.TunnelMapping{mapping}}); err != nil {
		t.Fatal("legacy state no longer readable", err)
	}
	if err := validateMapping(mapping); err == nil {
		t.Fatal("reserved PPP name accepted for a new configuration")
	}
	mapping.TunName = "tun101"
	if err := validateMapping(mapping); err != nil {
		t.Fatal("legacy mapping cannot be renamed", err)
	}
}

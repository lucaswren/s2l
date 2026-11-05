package validation

import (
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func TestEgressMustBeExplicitAndUnambiguous(t *testing.T) {
	for _, n := range []model.SocksNode{{Addr: "127.0.0.1:1080"}, {Egress: "socks5", Addr: "127.0.0.1:1080"}, {Egress: "wan", WANInterface: "eth0"}} {
		if err := Node(n); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []model.SocksNode{{Egress: "wan"}, {Egress: "wan", WANInterface: "tun101"}, {Egress: "wan", WANInterface: "lo"}, {Egress: "wan", WANInterface: "eth0", Addr: "127.0.0.1:1080"}, {Egress: "wan", WANInterface: "eth0", Password: "secret"}, {Egress: "socks5", WANInterface: "eth0", Addr: "127.0.0.1:1080"}, {Egress: "automatic", Addr: "127.0.0.1:1080"}} {
		if Node(n) == nil {
			t.Fatalf("ambiguous egress accepted: %+v", n)
		}
	}
}

func TestAbsentWANNameRemainsReservedAcrossBackupValidation(t *testing.T) {
	n := model.SocksNode{ID: "wan", Egress: "wan", WANInterface: "missingwan0"}
	m := model.TunnelMapping{ID: "m", NodeID: n.ID, TunName: n.WANInterface, TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "u", L2TPPass: "p"}
	if err := State(model.AppState{Nodes: []model.SocksNode{n}, Mappings: []model.TunnelMapping{m}}); err == nil {
		t.Fatal("backup could assign a TUN to an absent WAN's reserved name")
	}
	m.TunName = "tun101"
	if err := State(model.AppState{Nodes: []model.SocksNode{n}, Mappings: []model.TunnelMapping{m}}); err != nil {
		t.Fatal(err)
	}
}

package network

import (
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func TestPeerSnapshotRequiresRealPPPAndMatchesSubnet(t *testing.T) {
	input := `[{"ifname":"ppp0","ifindex":7,"mtu":1400,"txqlen":1000,"flags":["POINTOPOINT","UP"],"addr_info":[{"peer":"10.0.101.2/32"}]},{"ifname":"pppst123","flags":["UP"],"addr_info":[{"peer":"10.0.101.254/32"}]},{"ifname":"ppp1","flags":["POINTOPOINT"],"addr_info":[{"peer":"10.5.5.2/32"}]},{"ifname":"eth0","addr_info":[{"peer":"10.0.101.3/32"}]}]`
	rows, err := ParsePeers([]byte(input), []model.TunnelMapping{{ID: "m", Subnet: "10.0.101.0/24"}})
	if err != nil || len(rows) != 2 || rows[0].MappingID != "m" || rows[0].MTU != 1400 || rows[1].MappingID != "" {
		t.Fatal(rows, err)
	}
	if _, err = ParsePeers([]byte(`broken`), nil); err == nil {
		t.Fatal("invalid snapshot accepted")
	}
}

func TestPeerSnapshotIPRouteAddressField(t *testing.T) {
	input := `[{"ifname":"ppp0","ifindex":5,"mtu":1400,"flags":["POINTOPOINT","UP"],"addr_info":[{"family":"inet6","local":"fe80::1","address":"fe80::2"},{"family":"inet","local":"10.255.254.1","address":"10.0.101.2","prefixlen":32}]},{"ifname":"ppp1","flags":["POINTOPOINT"],"addr_info":[{"local":"10.0.101.1"}]}]`
	rows, err := ParsePeers([]byte(input), []model.TunnelMapping{{ID: "m", Subnet: "10.0.101.0/24"}})
	if err != nil || len(rows) != 1 || rows[0].PeerIP != "10.0.101.2" || rows[0].MappingID != "m" {
		t.Fatal(rows, err)
	}
}

package network

import (
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func TestValidateMapping(t *testing.T) {
	ok := model.TunnelMapping{
		TunName: "tun101",
		TableID: 101,
		Subnet:  "10.0.101.0/24",
	}
	if err := validateMapping(ok); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}

	cases := []model.TunnelMapping{
		{TableID: 101, Subnet: "10.0.101.0/24"},
		{TunName: "tun101", Subnet: "10.0.101.0/24"},
		{TunName: "tun101", TableID: 101},
		{TunName: "tun101", TableID: 101, Subnet: "not-a-cidr"},
	}
	for i, c := range cases {
		if err := validateMapping(c); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

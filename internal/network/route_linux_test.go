//go:build linux

package network

import (
	"github.com/lucaswren/s2l/internal/model"
	"testing"
)

func TestCleanupRejectsReservedTables(t *testing.T) {
	for _, id := range []int{253, 254, 255} {
		m := model.TunnelMapping{TunName: "tun101", TableID: id, Subnet: "10.0.101.0/24"}
		if err := DelPolicyRoute(m); err == nil {
			t.Fatalf("cleanup accepted reserved table %d", id)
		}
	}
}

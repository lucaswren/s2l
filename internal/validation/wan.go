package validation

import (
	"fmt"

	"github.com/lucaswren/s2l/internal/model"
)

// WANBindings reserves interface names even while their devices are absent.
// A managed TUN must never take over a configured WAN interface name.
func WANBindings(nodes []model.SocksNode, mappings []model.TunnelMapping) error {
	reserved := make(map[string]bool)
	for _, n := range nodes {
		if n.IsWAN() && n.WANInterface != "" {
			reserved[n.WANInterface] = true
		}
	}
	for _, m := range mappings {
		if reserved[m.TunName] {
			return fmt.Errorf("TUN name %s conflicts with a configured WAN interface", m.TunName)
		}
	}
	return nil
}

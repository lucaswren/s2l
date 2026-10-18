package validation

import (
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
)

func State(state model.AppState) error {
	nodes := map[string]bool{}
	for _, n := range state.Nodes {
		if n.ID == "" || nodes[n.ID] {
			return fmt.Errorf("backup contains missing or duplicate node IDs")
		}
		if err := Node(n); err != nil {
			return fmt.Errorf("invalid node %s: %w", n.ID, err)
		}
		nodes[n.ID] = true
	}
	ids := map[string]bool{}
	for i, m := range state.Mappings {
		if m.ID == "" || ids[m.ID] || !nodes[m.NodeID] {
			return fmt.Errorf("backup contains missing/duplicate mapping IDs or missing nodes")
		}
		ids[m.ID] = true
		if err := Mapping(m); err != nil {
			return fmt.Errorf("invalid mapping %s: %w", m.ID, err)
		}
		for _, other := range state.Mappings[:i] {
			if other.TunName == m.TunName || other.TableID == m.TableID || other.L2TPUser == m.L2TPUser || SubnetsOverlap(other.Subnet, m.Subnet) {
				return fmt.Errorf("mapping %s conflicts with %s", m.ID, other.ID)
			}
		}
	}
	return nil
}

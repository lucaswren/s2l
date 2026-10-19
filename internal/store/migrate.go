package store

import "fmt"

const tcpOnlyPolicyVersion = 1

// MigrateTCPOnlyPolicy runs once before tunnel restoration. Persist the marker
// and every mapping in one commit so failed writes can be retried on startup.
// Subsequent starts preserve any deliberate policy changes made after upgrade.
func (s *Store) MigrateTCPOnlyPolicy() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.NetworkPolicyVersion >= tcpOnlyPolicyVersion {
		return 0, nil
	}
	previous := cloneState(s.state)
	changed := 0
	for i := range s.state.Mappings {
		m := &s.state.Mappings[i]
		if m.UDPPolicy != "reject" || m.DNSMode != "auto" {
			changed++
		}
		m.UDPPolicy = "reject"
		m.DNSMode = "auto"
	}
	s.state.NetworkPolicyVersion = tcpOnlyPolicyVersion
	if err := s.saveOrRestoreLocked(previous); err != nil {
		return 0, fmt.Errorf("save TCP-only policy migration: %w", err)
	}
	return changed, nil
}

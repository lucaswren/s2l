package model

import "fmt"

func (m TunnelMapping) ValidateOptions() error {
	if mtu := m.EffectiveMTU(); mtu < 1280 || mtu > 1400 {
		return fmt.Errorf("mtu must be between 1280 and 1400")
	}
	if mss := m.EffectiveMSS(); mss < 536 || mss > m.EffectiveMTU()-40 {
		return fmt.Errorf("mss must be between 536 and mtu-40")
	}
	if n := m.EffectiveQueueLength(); n < 100 || n > 10000 {
		return fmt.Errorf("queue_length must be between 100 and 10000")
	}
	switch m.EffectiveUDPPolicy() {
	case "allow", "reject-quic", "reject":
	default:
		return fmt.Errorf("invalid udp_policy")
	}
	switch m.EffectiveDNSMode() {
	case "auto", "doh", "tcp":
	default:
		return fmt.Errorf("invalid dns_mode")
	}
	return nil
}

package model

import "fmt"

func (m TunnelMapping) ValidateOptions() error {
	if m.MaxSessions < 0 || m.MaxSessions > 128 {
		return fmt.Errorf("max_sessions must be between 0 and 128")
	}
	if m.UploadKbps < 0 || m.UploadKbps > 10000000 || m.DownloadKbps < 0 || m.DownloadKbps > 10000000 || m.MaxConnections < 0 || m.MaxConnections > 1000000 {
		return fmt.Errorf("bandwidth or connection limit out of range")
	}
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

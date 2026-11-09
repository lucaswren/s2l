package model

import (
	"fmt"
	"strings"
)

// ManagedLineSettings applies the fixed forwarding policy.
// Bandwidth, node identity, credentials and connection observation are preserved.
func (m TunnelMapping) ManagedLineSettings() TunnelMapping {
	m.MaxSessions = 1
	m.MTU, m.MSS, m.QueueLength = 1400, 1200, 1000
	m.UDPPolicy, m.DNSMode = "reject", "auto"
	m.DNSDoHURL, m.DNSTCPAddress = "", ""
	return m
}

func (m TunnelMapping) ValidateOptions() error {
	if m.MaxSessions < 0 || m.MaxSessions > 128 {
		return fmt.Errorf("max_sessions must be between 0 and 128")
	}
	if m.UploadKbps < 0 || m.UploadKbps > 10000000 || m.DownloadKbps < 0 || m.DownloadKbps > 10000000 {
		return fmt.Errorf("bandwidth limit out of range")
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
	return m.EffectiveDNSOptions().Validate()
}

// ValidateTUNNamespace applies to new configuration and activation. Legacy
// names must remain readable so the mapping can be stopped, renamed or removed.
func ValidateTUNNamespace(name string) error {
	if strings.HasPrefix(name, "ppp") {
		return fmt.Errorf("tun_name prefix ppp is reserved for PPP interfaces and diagnostic probes")
	}
	return nil
}

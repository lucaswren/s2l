package model

import (
	"fmt"
	"net"
)

// TunnelSubnet accepts complete, canonical RFC1918 networks with two peer IPs.
// The TUN transport pool is reserved and must never become a client subnet.
func TunnelSubnet(value string) (*net.IPNet, error) {
	ip, n, err := net.ParseCIDR(value)
	if err != nil || ip.To4() == nil {
		return nil, fmt.Errorf("subnet must be an IPv4 private network")
	}
	ones, bits := n.Mask.Size()
	if bits != 32 || ones > 30 || !ip.Equal(n.IP) {
		return nil, fmt.Errorf("subnet must be a canonical network with prefix /30 or shorter")
	}
	last := append(net.IP(nil), n.IP.To4()...)
	for i := range last {
		last[i] |= ^n.Mask[i]
	}
	if !n.IP.IsPrivate() || !last.IsPrivate() {
		return nil, fmt.Errorf("subnet must be entirely within an RFC1918 private network")
	}
	_, reserved, _ := net.ParseCIDR("172.19.0.0/16")
	if n.Contains(reserved.IP) || reserved.Contains(n.IP) {
		return nil, fmt.Errorf("subnet overlaps the reserved TUN address pool 172.19.0.0/16")
	}
	return n, nil
}

// One mapping assigns one fixed PPP peer address, so parallel sessions would
// duplicate that address regardless of the historical max_sessions setting.
func (m TunnelMapping) EffectiveMaxSessions() int { return 1 }

package model

import (
	"fmt"
	"net"
)

// ManagementAccess is an explicit exception for PPP ingress to local TCP services.
type ManagementAccess struct {
	Enabled       bool     `json:"enabled"`
	DestinationIP string   `json:"destination_ip"`
	SourceCIDRs   []string `json:"source_cidrs"`
	TCPPorts      []int    `json:"tcp_ports"`
}

func (a ManagementAccess) Validate() error {
	if !a.Enabled {
		return nil
	}
	ip := net.ParseIP(a.DestinationIP)
	if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
		return fmt.Errorf("management destination must be a unicast IPv4 address")
	}
	if len(a.SourceCIDRs) == 0 || len(a.SourceCIDRs) > 16 || len(a.TCPPorts) == 0 || len(a.TCPPorts) > 8 {
		return fmt.Errorf("specify 1–16 source IPv4 subnets and 1–8 TCP ports")
	}
	seen := map[int]bool{}
	for _, p := range a.TCPPorts {
		if p < 1 || p > 65535 || seen[p] {
			return fmt.Errorf("invalid or duplicate management TCP port")
		}
		seen[p] = true
	}
	seenCIDR := map[string]bool{}
	for _, s := range a.SourceCIDRs {
		ip, n, e := net.ParseCIDR(s)
		if e != nil || ip.To4() == nil || s != n.String() {
			return fmt.Errorf("management sources must be canonical IPv4 CIDRs")
		}
		ones, _ := n.Mask.Size()
		if ones < 8 || ip.IsLoopback() || ip.IsMulticast() || seenCIDR[s] {
			return fmt.Errorf("management source is too broad, duplicated or invalid")
		}
		seenCIDR[s] = true
	}
	return nil
}

//go:build linux

package network

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
)

const (
	commentPrefix = "s2l"
	tcpmssComment = "s2l-tcpmss"
)

// AddFirewall 为映射添加 NAT / FORWARD 规则：
//
//	iptables -t nat -A POSTROUTING -o <TunName> -j MASQUERADE
//	iptables -A FORWARD -s <Subnet> -o <TunName> -j ACCEPT
//	iptables -A FORWARD -o <TunName> -m state --state RELATED,ESTABLISHED -j ACCEPT
func AddFirewall(m model.TunnelMapping) error {
	if err := validateMapping(m); err != nil {
		return err
	}
	if err := DelFirewall(m); err != nil {
		return fmt.Errorf("clear old firewall rules: %w", err)
	}

	cmt := mappingComment(m)
	if err := run("iptables", "-I", "S2L_FORWARD", "1", "-i", "ppp+", "-s", m.Subnet, "-o", m.TunName, "-j", "ACCEPT", "-m", "comment", "--comment", cmt); err != nil {
		return err
	}
	// Reject at PPP ingress, before NAT and the userspace TUN stack. This
	// guarantees an ICMP error reaches the real client instead of relying on
	// a particular tun stack's error translation. DNS remains intercepted.
	if args := mappingUDPRejectArgs(m); len(args) > 0 {
		if err := run("iptables", append([]string{"-I", "S2L_FORWARD", "1"}, args...)...); err != nil {
			return errors.Join(err, DelFirewall(m))
		}
	}
	if args := connectionLimitArgs(m); len(args) > 0 {
		if err := run("iptables", append([]string{"-I", "S2L_FORWARD", "1"}, args...)...); err != nil {
			return errors.Join(err, DelFirewall(m))
		}
	}
	if err := run("iptables", "-t", "nat", "-A", "POSTROUTING",
		"-o", m.TunName, "-j", "MASQUERADE",
		"-m", "comment", "--comment", cmt,
	); err != nil {
		return errors.Join(err, DelFirewall(m))
	}
	if err := run("iptables", "-A", "FORWARD",
		"-s", m.Subnet, "-o", m.TunName, "-j", "ACCEPT",
		"-m", "comment", "--comment", cmt,
	); err != nil {
		return errors.Join(err, DelFirewall(m))
	}
	if err := run("iptables", "-A", "FORWARD",
		"-o", m.TunName, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT",
		"-m", "comment", "--comment", cmt,
	); err != nil {
		return errors.Join(err, DelFirewall(m))
	}
	// Accept related and established return traffic arriving through the TUN interface.
	if err := run("iptables", "-A", "FORWARD",
		"-i", m.TunName, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT",
		"-m", "comment", "--comment", cmt,
	); err != nil {
		return errors.Join(err, DelFirewall(m))
	}
	return nil
}

// DelFirewall 按 comment 删除该映射相关的 iptables 规则
func DelFirewall(m model.TunnelMapping) error {
	cmt := mappingComment(m)
	var udpErrors []error
	if args := connectionLimitArgs(m); len(args) > 0 {
		udpErrors = append(udpErrors, deleteFirewallRule(append([]string{"-D", "S2L_FORWARD"}, args...)...))
	}
	for _, policy := range []string{"reject", "reject-quic"} {
		copy := m
		copy.UDPPolicy = policy
		udpErrors = append(udpErrors, deleteFirewallRule(append([]string{"-D", "S2L_FORWARD"}, mappingUDPRejectArgs(copy)...)...))
	}
	return errors.Join(
		errors.Join(udpErrors...),
		deleteFirewallRule(mappingMSSDeleteArgs(m, "-i")...),
		deleteFirewallRule(mappingMSSDeleteArgs(m, "-o")...),
		deleteFirewallRule("-D", "S2L_FORWARD", "-i", "ppp+", "-s", m.Subnet, "-o", m.TunName, "-j", "ACCEPT", "-m", "comment", "--comment", cmt),
		deleteFirewallRule("-t", "nat", "-D", "POSTROUTING", "-o", m.TunName, "-j", "MASQUERADE", "-m", "comment", "--comment", cmt),
		deleteFirewallRule("-D", "FORWARD", "-s", m.Subnet, "-o", m.TunName, "-j", "ACCEPT", "-m", "comment", "--comment", cmt),
		deleteFirewallRule("-D", "FORWARD", "-o", m.TunName, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT", "-m", "comment", "--comment", cmt),
		deleteFirewallRule("-D", "FORWARD", "-i", m.TunName, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT", "-m", "comment", "--comment", cmt),
	)
}

func mappingUDPRejectArgs(m model.TunnelMapping) []string {
	args := []string{"-i", "ppp+", "-s", m.Subnet, "-o", m.TunName, "-p", "udp"}
	switch m.EffectiveUDPPolicy() {
	case "allow":
		return nil
	case "reject-quic":
		args = append(args, "--dport", "443")
	default:
		args = append(args, "!", "--dport", "53")
	}
	return append(args, "-j", "REJECT", "--reject-with", "icmp-port-unreachable", "-m", "comment", "--comment", mappingComment(m)+"-udp")
}

func mappingMSSArgs(m model.TunnelMapping, direction string) []string {
	source := "-s"
	if direction == "-o" {
		source = "-d"
	}
	return []string{direction, "ppp+", source, m.Subnet, "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--set-mss", fmt.Sprint(m.EffectiveMSS()), "-m", "comment", "--comment", mappingComment(m) + "-mss"}
}
func mappingMSSDeleteArgs(m model.TunnelMapping, direction string) []string {
	return append([]string{"-t", "mangle", "-D", "FORWARD"}, mappingMSSArgs(m, direction)...)
}
func addMappingTCPMSS(m model.TunnelMapping) error {
	for _, direction := range []string{"-i", "-o"} {
		args := mappingMSSArgs(m, direction)
		if run("iptables", append([]string{"-t", "mangle", "-C", "FORWARD"}, args...)...) == nil {
			continue
		}
		if err := run("iptables", append([]string{"-t", "mangle", "-I", "FORWARD", "1"}, args...)...); err != nil {
			return err
		}
	}
	return nil
}

func deleteFirewallRule(args ...string) error {
	for i := 0; i < 8; i++ {
		if err := run("iptables", args...); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "bad rule") || strings.Contains(msg, "no such file or directory") {
				return nil
			}
			return err
		}
	}
	return nil
}

func tcpMSSArgs(direction string) []string {
	return []string{direction, "ppp+", "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
		"-j", "TCPMSS", "--set-mss", "1200",
		"-m", "comment", "--comment", tcpmssComment}
}

// RemoveTCPMSS also removes legacy filter rules when no mappings remain.
func RemoveTCPMSS() {
	for _, direction := range []string{"-i", "-o"} {
		for i := 0; i < 8; i++ {
			if err := run("iptables", append([]string{"-t", "mangle", "-D", "FORWARD"}, tcpMSSArgs(direction)...)...); err != nil {
				break
			}
		}
	}
	for _, mss := range []string{"1200", "1320", "1360"} {
		for i := 0; i < 4; i++ {
			if err := run("iptables", "-D", "FORWARD",
				"-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
				"-j", "TCPMSS", "--set-mss", mss,
				"-m", "comment", "--comment", tcpmssComment,
			); err != nil {
				break
			}
		}
	}
	for i := 0; i < 4; i++ {
		if err := run("iptables", "-D", "FORWARD",
			"-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
			"-j", "TCPMSS", "--clamp-mss-to-pmtu",
			"-m", "comment", "--comment", tcpmssComment,
		); err != nil {
			break
		}
	}
}

func mappingComment(m model.TunnelMapping) string {
	id := m.ID
	if id == "" {
		id = m.TunName
	}
	// iptables comment 最长 256，保持简短
	return fmt.Sprintf("%s:%s", commentPrefix, id)
}

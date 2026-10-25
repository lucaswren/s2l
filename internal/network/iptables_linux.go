//go:build linux

package network

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
)

const (
	commentPrefix = "s2l"
	tcpmssComment = "s2l-tcpmss"
)

// AddFirewall permits PPP traffic only through its fixed TUN and permits TUN
// replies only back to that mapping's PPP subnet. Both directions fail closed.
func AddFirewall(m model.TunnelMapping) (result error) {
	if err := validateMapping(m); err != nil {
		return err
	}
	// Install before removing legacy broad return allowances. This hook and the
	// terminal DROP survive teardown, including packets queued during PPP exit.
	// Key the hook by interface, so reconnects never accumulate duplicate rules.
	if err := ensureRule("iptables", "-I", "FORWARD", "-i", m.TunName, "-j", "S2L_RETURN"); err != nil {
		return err
	}
	if err := DelFirewall(m); err != nil {
		return fmt.Errorf("clear old firewall rules: %w", err)
	}
	defer func() {
		if result != nil {
			result = errors.Join(result, DelFirewall(m))
		}
	}()

	cmt := mappingComment(m)
	// A first packet accepted before MASQUERADE can create a persistent
	// un-NATed conntrack entry. Keep ingress closed until the entire path exists.
	if err := addMappingTCPMSS(m); err != nil {
		return err
	}
	if err := run("iptables", "-t", "nat", "-A", "POSTROUTING", "-o", m.TunName, "-j", "MASQUERADE", "-m", "comment", "--comment", cmt); err != nil {
		return err
	}
	if err := run("iptables", append([]string{"-I", "S2L_RETURN", "1"}, mappingReturnArgs(m)...)...); err != nil {
		return err
	}
	acceptPosition := 1
	// Reject at PPP ingress, before NAT and the userspace TUN stack. This
	// guarantees an ICMP error reaches the real client instead of relying on
	// a particular tun stack's error translation. DNS remains intercepted.
	if args := mappingUDPRejectArgs(m); len(args) > 0 {
		if err := run("iptables", append([]string{"-I", "S2L_FORWARD", "1"}, args...)...); err != nil {
			return err
		}
		acceptPosition++
	}
	if args := connectionLimitArgs(m); len(args) > 0 {
		if err := run("iptables", append([]string{"-I", "S2L_FORWARD", "1"}, args...)...); err != nil {
			return err
		}
		acceptPosition++
	}
	// Keep this mapping's UDP/connection restrictions ahead of its allowance.
	return run("iptables", "-I", "S2L_FORWARD", strconv.Itoa(acceptPosition), "-i", "ppp+", "-s", m.Subnet, "-o", m.TunName, "-j", "ACCEPT", "-m", "comment", "--comment", cmt)
}

// DelFirewall 按 comment 删除该映射相关的 iptables 规则
func DelFirewall(m model.TunnelMapping) error {
	cmt := mappingComment(m)
	// Close ingress before removing NAT, MSS or restrictions, including on rollback.
	if err := deleteFirewallRule("-D", "S2L_FORWARD", "-i", "ppp+", "-s", m.Subnet, "-o", m.TunName, "-j", "ACCEPT", "-m", "comment", "--comment", cmt); err != nil {
		return err
	}
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
		deleteFirewallRule(append([]string{"-D", "S2L_RETURN"}, mappingReturnArgs(m)...)...),
		deleteFirewallRule(mappingMSSDeleteArgs(m, "-i")...),
		deleteFirewallRule(mappingMSSDeleteArgs(m, "-o")...),
		deleteFirewallRule("-t", "nat", "-D", "POSTROUTING", "-o", m.TunName, "-j", "MASQUERADE", "-m", "comment", "--comment", cmt),
		deleteFirewallRule("-D", "FORWARD", "-s", m.Subnet, "-o", m.TunName, "-j", "ACCEPT", "-m", "comment", "--comment", cmt),
		deleteFirewallRule("-D", "FORWARD", "-o", m.TunName, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT", "-m", "comment", "--comment", cmt),
		deleteFirewallRule("-D", "FORWARD", "-i", m.TunName, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT", "-m", "comment", "--comment", cmt),
	)
}

func mappingReturnArgs(m model.TunnelMapping) []string {
	return []string{"-i", m.TunName, "-o", "ppp+", "-d", m.Subnet, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT", "-m", "comment", "--comment", mappingComment(m)}
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
			if IsMissingState(missingFirewall(err, "iptables", args)) || strings.Contains(msg, "no such file or directory") {
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

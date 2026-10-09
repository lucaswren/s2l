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
	if err := run("iptables", "-t", "nat", "-A", "POSTROUTING",
		"-o", m.TunName, "-j", "MASQUERADE",
		"-m", "comment", "--comment", cmt,
	); err != nil {
		return err
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
	// 回程：从 TUN 进入的 RELATED/ESTABLISHED（PRD 未写，但实际转发必需）
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
	return errors.Join(
		deleteFirewallRule("-t", "nat", "-D", "POSTROUTING", "-o", m.TunName, "-j", "MASQUERADE", "-m", "comment", "--comment", cmt),
		deleteFirewallRule("-D", "FORWARD", "-s", m.Subnet, "-o", m.TunName, "-j", "ACCEPT", "-m", "comment", "--comment", cmt),
		deleteFirewallRule("-D", "FORWARD", "-o", m.TunName, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT", "-m", "comment", "--comment", cmt),
		deleteFirewallRule("-D", "FORWARD", "-i", m.TunName, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT", "-m", "comment", "--comment", cmt),
	)
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

// EnsureTCPMSS 钳位 TCP MSS，减轻 L2TP+SOCKS 分片黑洞（不改客户端 MTU）。
func EnsureTCPMSS() error {
	RemoveTCPMSS()
	return run("iptables", "-I", "FORWARD", "1",
		"-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
		"-j", "TCPMSS", "--set-mss", "1200",
		"-m", "comment", "--comment", tcpmssComment,
	)
}

// RemoveTCPMSS 删除全局 TCPMSS 规则（无活跃映射时调用）
func RemoveTCPMSS() {
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

func hasTCPMSS() bool {
	out, err := output("iptables", "-S", "FORWARD")
	if err != nil {
		return false
	}
	return strings.Contains(out, tcpmssComment)
}

func mappingComment(m model.TunnelMapping) string {
	id := m.ID
	if id == "" {
		id = m.TunName
	}
	// iptables comment 最长 256，保持简短
	return fmt.Sprintf("%s:%s", commentPrefix, id)
}

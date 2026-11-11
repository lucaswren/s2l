//go:build linux

package network

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
)

const tunEgressComment = "s2l:tun-egress"

var tunEgressRule = regexp.MustCompile(`^-A POSTROUTING -s 172\.19\.[0-9]{1,3}\.1/32 ! -o [A-Za-z0-9_.-]{1,15} -m comment --comment "?s2l:tun-egress"? -j DROP$`)

func tunEgressArgs(m model.TunnelMapping) []string {
	return []string{"POSTROUTING", "-s", m.TUNAddress().Addr().String() + "/32", "!", "-o", m.TunName, "-m", "comment", "--comment", tunEgressComment, "-j", "DROP"}
}

// The system TUN stack uses a local TCP listener. Locally generated resets
// can bypass FORWARD. Check the selected output before source NAT on both paths.
func ensureTUNEgress(m model.TunnelMapping) error {
	args := tunEgressArgs(m)
	check := append([]string{"-t", "mangle", "-C"}, args...)
	if err := run("iptables", check...); err != nil {
		if !IsMissingState(missingFirewall(err, "iptables", check)) {
			return err
		}
		return run("iptables", append([]string{"-t", "mangle", "-I"}, args...)...)
	}
	return nil
}

// Remove the guard after sing-box closes the TUN. Keep it if a failed stop
// leaves the interface present, so queued traffic cannot escape.
func (c *Controller) ReleaseTUNEgress(m model.TunnelMapping) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateMapping(m); err != nil {
		return err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, iface := range interfaces {
		if iface.Name == m.TunName {
			return fmt.Errorf("TUN interface %s still present; egress guard retained", m.TunName)
		}
	}
	c.rulesRevision++
	return deleteFirewallRule(append([]string{"-t", "mangle", "-D"}, tunEgressArgs(m)...)...)
}

func tunEgressOrderPlan(snapshot string) string {
	var owned []string
	seen := make(map[string]bool)
	foreign, reorder := false, false
	for _, line := range strings.Split(snapshot, "\n") {
		if !strings.HasPrefix(line, "-A POSTROUTING ") {
			continue
		}
		if !tunEgressRule.MatchString(line) {
			foreign = true
			continue
		}
		reorder = reorder || foreign || seen[line]
		owned = append(owned, line)
		seen[line] = true
	}
	if !reorder {
		return ""
	}
	var plan strings.Builder
	plan.WriteString("*mangle\n")
	for _, line := range owned {
		plan.WriteString("-D" + line[2:] + "\n")
	}
	for i := len(owned) - 1; i >= 0; i-- {
		line := owned[i]
		if seen[line] {
			plan.WriteString("-I" + line[2:] + "\n")
			delete(seen, line)
		}
	}
	plan.WriteString("COMMIT\n")
	return plan.String()
}

func repairTUNEgressOrder(ctx context.Context) error {
	snapshot, err := outputContext(ctx, "iptables", "-t", "mangle", "-S", "POSTROUTING")
	if err != nil {
		return err
	}
	if plan := tunEgressOrderPlan(snapshot); plan != "" {
		cmd := exec.CommandContext(ctx, "iptables-restore", "-w", "5", "--noflush")
		cmd.Stdin = strings.NewReader(plan)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("restore TUN egress guard order: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

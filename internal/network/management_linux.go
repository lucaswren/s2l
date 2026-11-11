//go:build linux

package network

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

const managementChain = "S2L_MANAGEMENT"

func (c *Controller) SetManagementAccess(a model.ManagementAccess) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.Enabled {
		found := false
		addresses, e := net.InterfaceAddrs()
		if e != nil {
			return e
		}
		for _, addr := range addresses {
			ip, _, e := net.ParseCIDR(addr.String())
			if e == nil && ip.Equal(net.ParseIP(a.DestinationIP)) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("管理目标 IP 未绑定服务器本机，请先核对公网 NAT 和本机地址")
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rulesRevision++
	if err := ensureGuard(a); err != nil {
		return err
	}
	a.SourceCIDRs = append([]string(nil), a.SourceCIDRs...)
	a.TCPPorts = append([]int(nil), a.TCPPorts...)
	c.management = a
	return nil
}

const managementJump = "-A INPUT -i ppp+ -j " + managementChain

func managementEntries(a model.ManagementAccess) []string {
	var entries []string
	if a.Enabled {
		for _, source := range a.SourceCIDRs {
			for _, port := range a.TCPPorts {
				entries = append(entries, fmt.Sprintf("-A %s -s %s -d %s -p tcp --dport %d -m conntrack --ctstate NEW,ESTABLISHED -j ACCEPT", managementChain, source, a.DestinationIP, port))
			}
		}
	}
	return append(entries, "-A "+managementChain+" -j DROP")
}

func managementRules(a model.ManagementAccess, hasJump bool) string {
	var b strings.Builder
	b.WriteString("*filter\n:" + managementChain + " - [0:0]\n-F " + managementChain + "\n")
	for _, rule := range managementEntries(a) {
		b.WriteString(rule + "\n")
	}
	if hasJump {
		b.WriteString("-D" + managementJump[2:] + "\n")
	}
	b.WriteString("-I INPUT 1 -i ppp+ -j " + managementChain + "\nCOMMIT\n")
	return b.String()
}

func managementMatches(snapshot string, a model.ManagementAccess) bool {
	expected := managementEntries(a)
	jumps, index := 0, 0
	for _, line := range strings.Split(snapshot, "\n") {
		line = strings.TrimSpace(line)
		if line == managementJump {
			jumps++
		}
		if strings.HasPrefix(line, "-A "+managementChain+" ") {
			if index == len(expected) || mappingGuardRuleKey(line) != mappingGuardRuleKey(expected[index]) {
				return false
			}
			index++
		}
	}
	return jumps == 1 && index == len(expected)
}

func ensureManagementAccess(a model.ManagementAccess) error {
	return ensureManagementAccessContext(context.Background(), a)
}

func ensureManagementAccessContext(ctx context.Context, a model.ManagementAccess) error {
	if err := a.Validate(); err != nil {
		return err
	}
	snapshot, err := outputContext(ctx, "iptables", "-S")
	if err != nil {
		return err
	}
	jumps := 0
	for _, line := range strings.Split(snapshot, "\n") {
		if strings.TrimSpace(line) == managementJump {
			jumps++
		}
	}
	plan := managementRules(a, false)
	plan = strings.Replace(plan, "-I INPUT", strings.Repeat("-D"+managementJump[2:]+"\n", jumps)+"-I INPUT", 1)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "iptables-restore", "-w", "5", "--noflush")
	cmd.Stdin = strings.NewReader(plan)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apply PPP management exceptions: %w", err)
	}
	return nil
}

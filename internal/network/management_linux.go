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
	if err := ensureGuard(a); err != nil {
		return err
	}
	a.SourceCIDRs = append([]string(nil), a.SourceCIDRs...)
	a.TCPPorts = append([]int(nil), a.TCPPorts...)
	c.management = a
	return nil
}
func managementRules(a model.ManagementAccess, hasJump bool) string {
	var b strings.Builder
	b.WriteString("*filter\n:" + managementChain + " - [0:0]\n-F " + managementChain + "\n")
	if a.Enabled {
		for _, source := range a.SourceCIDRs {
			for _, p := range a.TCPPorts {
				fmt.Fprintf(&b, "-A %s -s %s -d %s -p tcp --dport %d -m conntrack --ctstate NEW,ESTABLISHED -j ACCEPT\n", managementChain, source, a.DestinationIP, p)
			}
		}
	}
	b.WriteString("-A " + managementChain + " -j DROP\n")
	if hasJump {
		b.WriteString("-D INPUT -i ppp+ -j " + managementChain + "\n")
	}
	b.WriteString("-I INPUT 1 -i ppp+ -j " + managementChain + "\nCOMMIT\n")
	return b.String()
}
func ensureManagementAccess(a model.ManagementAccess) error {
	if err := a.Validate(); err != nil {
		return err
	}
	hasJump := run("iptables", "-C", "INPUT", "-i", "ppp+", "-j", managementChain) == nil
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "iptables-restore", "-w", "5", "--noflush")
	cmd.Stdin = strings.NewReader(managementRules(a, hasJump))
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apply PPP management exceptions: %w", err)
	}
	return nil
}

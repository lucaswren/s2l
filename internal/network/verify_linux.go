//go:build linux

package network

import (
	"encoding/json"
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"net"
	"os"
	"strconv"
	"strings"
)

// Verify checks the kernel forwarding path without mutating it.
func (c *Controller) Verify(m model.TunnelMapping) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateMapping(m); err != nil {
		return err
	}
	rules, err := output("ip", "-N", "-j", "rule", "show")
	if err != nil {
		return err
	}
	routes, err := output("ip", "-j", "route", "show", "table", strconv.Itoa(m.TableID))
	if err != nil {
		return err
	}
	if err := verifyRouteJSON(m, []byte(rules), []byte(routes)); err != nil {
		return err
	}
	checks := [][]string{
		{"-C", "FORWARD", "-i", "ppp+", "-j", "S2L_FORWARD"},
		{"-C", "INPUT", "-i", "ppp+", "-j", "DROP"},
		{"-C", "S2L_FORWARD", "-j", "DROP"},
		{"-C", "S2L_FORWARD", "-i", "ppp+", "-s", m.Subnet, "-o", m.TunName, "-j", "ACCEPT", "-m", "comment", "--comment", mappingComment(m)},
		{"-t", "nat", "-C", "POSTROUTING", "-o", m.TunName, "-j", "MASQUERADE", "-m", "comment", "--comment", mappingComment(m)},
		{"-C", "FORWARD", "-i", m.TunName, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT", "-m", "comment", "--comment", mappingComment(m)},
	}
	for _, direction := range []string{"-i", "-o"} {
		checks = append(checks, append([]string{"-t", "mangle", "-C", "FORWARD"}, mappingMSSArgs(m, direction)...))
	}
	for _, args := range checks {
		if err := run("iptables", args...); err != nil {
			return fmt.Errorf("forwarding rule missing: %w", err)
		}
	}
	for _, chain := range []string{"INPUT", "FORWARD"} {
		if err := run("ip6tables", "-C", chain, "-i", "ppp+", "-j", "DROP"); err != nil {
			return err
		}
	}
	enabled, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(enabled)) != "1" {
		return fmt.Errorf("IPv4 forwarding disabled")
	}
	return nil
}

func verifyRouteJSON(m model.TunnelMapping, rules, routes []byte) error {
	var ruleList []struct {
		Src      string `json:"src"`
		SrcLen   *int   `json:"srclen"`
		Table    any    `json:"table"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal(rules, &ruleList); err != nil {
		return err
	}
	found := false
	for _, r := range ruleList {
		source := r.Src
		if r.SrcLen != nil && !strings.Contains(source, "/") {
			source = fmt.Sprintf("%s/%d", source, *r.SrcLen)
		}
		if canonicalSubnet(source) == canonicalSubnet(m.Subnet) && fmt.Sprint(r.Table) == strconv.Itoa(m.TableID) && r.Priority == 10000+m.TableID {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("policy rule missing")
	}
	var routeList []struct {
		Dst    string `json:"dst"`
		Dev    string `json:"dev"`
		Type   string `json:"type"`
		Metric int    `json:"metric"`
	}
	if err := json.Unmarshal(routes, &routeList); err != nil {
		return err
	}
	live, guard := false, false
	for _, r := range routeList {
		if r.Dst == "default" {
			if r.Dev == m.TunName && r.Metric == 100 && (r.Type == "" || r.Type == "unicast") {
				live = true
			}
			if r.Type == "unreachable" && r.Metric == 32767 {
				guard = true
			}
		}
	}
	if !live || !guard {
		return fmt.Errorf("TUN route or unreachable fallback missing")
	}
	return nil
}

func canonicalSubnet(value string) string {
	_, n, err := net.ParseCIDR(value)
	if err != nil {
		return value
	}
	return n.String()
}

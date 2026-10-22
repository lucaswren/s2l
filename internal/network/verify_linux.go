//go:build linux

package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/lucaswren/s2l/internal/lockutil"
	"github.com/lucaswren/s2l/internal/model"
)

// Verify checks the kernel forwarding path without mutating it.
func (c *Controller) Verify(m model.TunnelMapping) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return c.VerifyContext(ctx, m)
}

func (c *Controller) VerifyContext(ctx context.Context, m model.TunnelMapping) error {
	if err := lockutil.Lock(ctx, &c.mu); err != nil {
		return err
	}
	defer c.mu.Unlock()
	return c.verifyContext(ctx, m)
}

func (c *Controller) verifyContext(ctx context.Context, m model.TunnelMapping) error {
	run := func(name string, args ...string) error { return runContext(ctx, name, args...) }
	output := func(name string, args ...string) (string, error) { return outputContext(ctx, name, args...) }
	if err := validateMapping(m); err != nil {
		return err
	}
	iface, err := net.InterfaceByName(m.TunName)
	if err != nil {
		return err
	}
	if iface.Flags&net.FlagUp == 0 {
		return missingState("TUN interface down", "ip", "link", "set", "dev", m.TunName, "up")
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
	if args := mappingUDPRejectArgs(m); len(args) > 0 {
		checks = append(checks, append([]string{"-C", "S2L_FORWARD"}, args...))
	}
	if args := connectionLimitArgs(m); len(args) > 0 {
		checks = append(checks, append([]string{"-C", "S2L_FORWARD"}, args...))
	}
	for _, args := range checks {
		if err := run("iptables", args...); err != nil {
			return missingFirewall(err, "iptables", args)
		}
	}
	for _, chain := range []string{"INPUT", "FORWARD"} {
		if err := run("ip6tables", "-C", chain, "-i", "ppp+", "-j", "DROP"); err != nil {
			return missingFirewall(err, "ip6tables", []string{"-C", chain, "-i", "ppp+", "-j", "DROP"})
		}
	}
	enabled, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(enabled)) != "1" {
		return missingState("IPv4 forwarding disabled", "sysctl", "-w", "net.ipv4.ip_forward=1")
	}
	return verifyBandwidth(ctx, m)
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
	if !guard {
		return missingState("unreachable fallback missing", "ip", "route", "replace", "unreachable", "default", "table", strconv.Itoa(m.TableID), "metric", "32767", "protocol", routeProtocol)
	}
	if !found {
		return missingState("policy rule missing", "ip", "rule", "add", "from", m.Subnet, "table", strconv.Itoa(m.TableID), "priority", strconv.Itoa(10000+m.TableID), "protocol", routeProtocol)
	}
	if !live {
		return missingState("TUN route missing", "ip", "route", "replace", "default", "dev", m.TunName, "table", strconv.Itoa(m.TableID), "metric", "100", "protocol", routeProtocol)
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

func missingFirewall(err error, command string, args []string) error {
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return err
	}
	text := strings.ToLower(err.Error())
	if !strings.Contains(text, "bad rule") && !strings.Contains(text, "does a matching rule exist") && !strings.Contains(text, "no chain/target/match") {
		return err
	}
	return missingState("forwarding rule missing: "+err.Error(), command, args...)
}

// RepairContext rechecks under lock and repairs missing components in place.
// Healthy routes, rules, processes and fixed upstreams are never rebuilt.
func (c *Controller) RepairContext(ctx context.Context, m model.TunnelMapping) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := lockutil.Lock(ctx, &c.mu); err != nil {
		return err
	}
	defer c.mu.Unlock()
	for attempts := 0; attempts < 24; attempts++ {
		err := c.verifyContext(ctx, m)
		if err == nil {
			return nil
		}
		var missing *MissingState
		if !errors.As(err, &missing) {
			return err
		}
		switch missing.command {
		case "iptables", "ip6tables":
			args := append([]string(nil), missing.args...)
			check := 0
			for i, arg := range args {
				if arg == "-C" {
					check = i
					break
				}
			}
			chain := args[check+1]
			if chain == "S2L_FORWARD" && strings.Contains(strings.Join(args, " "), "-j ACCEPT") {
				snapshot, e := outputContext(ctx, "iptables", "-S", "S2L_FORWARD")
				if e != nil {
					return e
				}
				position, terminal := 0, false
				for _, line := range strings.Split(snapshot, "\n") {
					if strings.HasPrefix(line, "-A S2L_FORWARD ") {
						position++
						if strings.TrimSpace(line) == "-A S2L_FORWARD -j DROP" {
							terminal = true
							break
						}
					}
				}
				if !terminal {
					return fmt.Errorf("terminal guard unavailable")
				}
				args[check] = "-I"
				args = append(args[:check+2], append([]string{strconv.Itoa(position)}, args[check+2:]...)...)
			} else if chain == "S2L_FORWARD" && len(args) == 4 {
				args[check] = "-A" // Restore only the terminal guard, after existing rules.
			} else {
				position := 1
				if missing.command == "iptables" && chain == "FORWARD" && args[len(args)-1] == "S2L_FORWARD" {
					if _, err := outputContext(ctx, "iptables", "-S", "S2L_FORWARD"); err != nil {
						if !IsMissingState(missingFirewall(err, "iptables", nil)) {
							return err
						}
						if err := runContext(ctx, "iptables", "-N", "S2L_FORWARD"); err != nil {
							return err
						}
						if err := runContext(ctx, "iptables", "-A", "S2L_FORWARD", "-j", "DROP"); err != nil {
							return err
						}
					}
				}
				if missing.command == "iptables" && chain == "INPUT" {
					snapshot, err := outputContext(ctx, "iptables", "-S", "INPUT")
					if err != nil {
						return err
					}
					index := 0
					for _, line := range strings.Split(snapshot, "\n") {
						if strings.HasPrefix(line, "-A INPUT ") {
							index++
							if strings.TrimSpace(line) == "-A INPUT -i ppp+ -j "+managementChain {
								position = index + 1
							}
						}
					}
				}
				args[check] = "-I"
				args = append(args[:check+2], append([]string{strconv.Itoa(position)}, args[check+2:]...)...)
			}
			if e := runContext(ctx, missing.command, args...); e != nil {
				return e
			}
		case "bandwidth-upload", "bandwidth-download":
			limit := m
			if missing.command == "bandwidth-upload" {
				limit.DownloadKbps = 0
			} else {
				limit.UploadKbps = 0
			}
			if e := applyBandwidthContext(ctx, limit); e != nil {
				return e
			}
		default:
			if e := runContext(ctx, missing.command, missing.args...); e != nil {
				return e
			}
		}
	}
	return fmt.Errorf("forwarding repair exceeded component limit")
}

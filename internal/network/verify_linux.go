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
	"path/filepath"
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
	routes, err := output("ip", "-N", "-j", "route", "show", "table", strconv.Itoa(m.TableID))
	if err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "fib table does not exist") {
			return err
		}
		// An absent FIB is an inspected empty table, not an inspection failure.
		// verifyRouteJSON must still validate rule ownership before authorizing
		// repair, just as routeSnapshot does during setup and cleanup.
		routes = "[]"
	}
	if err := verifyRouteJSON(m, []byte(rules), []byte(routes)); err != nil {
		return err
	}
	checks := [][]string{
		{"-C", "FORWARD", "-i", m.TunName, "-j", "S2L_RETURN"},
		{"-C", "S2L_RETURN", "-j", "DROP"},
		{"-C", "FORWARD", "-i", "ppp+", "-j", "S2L_FORWARD"},
		{"-C", "INPUT", "-i", "ppp+", "-j", "DROP"},
		{"-C", "S2L_FORWARD", "-j", "DROP"},
		{"-t", "nat", "-C", "POSTROUTING", "-o", m.TunName, "-j", "MASQUERADE", "-m", "comment", "--comment", mappingComment(m)},
		append([]string{"-C", "S2L_RETURN"}, mappingReturnArgs(m)...),
	}
	for _, direction := range []string{"-i", "-o"} {
		checks = append(checks, append([]string{"-t", "mangle", "-C", "FORWARD"}, mappingMSSArgs(m, direction)...))
	}
	if args := mappingUDPRejectArgs(m); len(args) > 0 {
		checks = append(checks, append([]string{"-C", "S2L_FORWARD"}, args...))
	}
	checks = append(checks, append([]string{"-C", "S2L_FORWARD"}, mappingForwardArgs(m)...))
	if c.sessionGuard {
		checks = append(checks, sessionFirewallRules()...)
	}
	// Read each filter table once. Use its rules for presence and order checks.
	ipv4, err := output("iptables", "-S")
	if err != nil {
		return err
	}
	ipv6, err := output("ip6tables", "-S")
	if err != nil {
		return err
	}
	rules4, rules6 := filterRuleKeys(ipv4), filterRuleKeys(ipv6)
	for _, args := range checks {
		if args[0] == "-C" && rules4[mappingGuardRuleKey("-A "+strings.Join(args[1:], " "))] {
			continue
		}
		// Keep the exact kernel check for other tables and ambiguous/absent
		// snapshot matches. Failed inspections must never authorize repair.
		if err := run("iptables", args...); err != nil {
			return missingFirewall(err, "iptables", args)
		}
	}
	for _, chain := range []string{"INPUT", "FORWARD"} {
		args := []string{"-C", chain, "-i", "ppp+", "-j", "DROP"}
		if !rules6["-A "+chain+" -i ppp+ -j DROP"] {
			if err := run("ip6tables", args...); err != nil {
				return missingFirewall(err, "ip6tables", args)
			}
		}
	}
	if guardOrderPlan(ipv4, false) != "" || guardOrderPlan(ipv6, true) != "" {
		return missingState("PPP guard hooks are shadowed or duplicated", "guard-order")
	}
	if !managementMatches(ipv4, c.management) {
		return missingState("PPP management exceptions differ from configuration", "management-guard")
	}
	if err := c.checkMappingGuardOrder(ctx, m, ipv4, false); err != nil {
		return err
	}
	if c.sessionGuard {
		if err := c.verifySessionGuard(ctx); err != nil {
			return err
		}
		if owner, ok := c.admitted[m.ID]; ok {
			if err := verifySessionReturn(m, owner, []byte(rules), []byte(routes)); err != nil {
				return err
			}
		}
	}
	enabled, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(enabled)) != "1" {
		return missingState("IPv4 forwarding disabled", "sysctl", "-w", "net.ipv4.ip_forward=1")
	}
	// Linux uses max(all, interface) for rp_filter. Checking only PPP tuning
	// misses global/TUN changes made by network services after initial setup.
	for _, name := range []string{"all", "default", m.TunName} {
		value, err := os.ReadFile(filepath.Join("/proc/sys/net/ipv4/conf", name, "rp_filter"))
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(value)) != "0" {
			return missingState("reverse path filtering enabled on "+name, "sysctl", "-w", "net/ipv4/conf/"+name+"/rp_filter=0")
		}
	}
	link, err := output("ip", "-j", "link", "show", "dev", m.TunName)
	if err != nil {
		return err
	}
	if err := verifyTUNParameters(m, []byte(link)); err != nil {
		return err
	}
	return verifyBandwidth(ctx, m)
}

func filterRuleKeys(snapshot string) map[string]bool {
	rules := make(map[string]bool)
	for _, line := range strings.Split(snapshot, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "-A ") {
			rules[mappingGuardRuleKey(line)] = true
		}
	}
	return rules
}

func verifyTUNParameters(m model.TunnelMapping, data []byte) error {
	var links []struct {
		Name  string `json:"ifname"`
		MTU   int    `json:"mtu"`
		Queue int    `json:"txqlen"`
	}
	if err := json.Unmarshal(data, &links); err != nil {
		return err
	}
	if len(links) != 1 || links[0].Name != m.TunName || links[0].MTU <= 0 {
		return fmt.Errorf("invalid TUN link snapshot")
	}
	if links[0].MTU != m.EffectiveMTU() || links[0].Queue != m.EffectiveQueueLength() {
		return missingState("TUN MTU or transmit queue length differs from configuration",
			"ip", "link", "set", "dev", m.TunName, "mtu", strconv.Itoa(m.EffectiveMTU()),
			"txqueuelen", strconv.Itoa(m.EffectiveQueueLength()))
	}
	return nil
}

func verifyRouteJSON(m model.TunnelMapping, rules, routes []byte) error {
	// Existence alone is insufficient: an extra more-specific route, gateway,
	// fwmark selector or rule at this priority can silently override this path.
	var ownedRules []resourceRule
	var ownedRoutes []resourceRoute
	if err := json.Unmarshal(rules, &ownedRules); err != nil {
		return err
	}
	if err := json.Unmarshal(routes, &ownedRoutes); err != nil {
		return err
	}
	ownedMapping := m
	ownedMapping.Subnet = canonicalSubnet(m.Subnet)
	if err := validateRouteOwnership(ownedMapping, ownedRules, ownedRoutes); err != nil {
		return err
	}
	found := false
	for _, r := range ownedRules {
		source := r.Src
		if r.SrcLen != nil && !strings.Contains(source, "/") {
			source = fmt.Sprintf("%s/%d", source, *r.SrcLen)
		}
		if canonicalSubnet(source) == canonicalSubnet(m.Subnet) && fmt.Sprint(r.Table) == strconv.Itoa(m.TableID) && r.Priority == 10000+m.TableID {
			found = true
		}
	}

	live, guard := false, false
	for _, r := range ownedRoutes {
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
		case "mapping-guard-order":
			if e := c.checkMappingGuardOrder(ctx, m, "", true); e != nil {
				return e
			}
		case "management-guard":
			if e := ensureManagementAccessContext(ctx, c.management); e != nil {
				return e
			}
		case "session-guard":
			if e := c.commitSessionGuard(ctx, c.admitted); e != nil {
				return e
			}
		case "guard-order":
			if e := checkGuardOrder(ctx, true); e != nil {
				return e
			}
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
			guardChain := chain == "S2L_FORWARD" || chain == "S2L_RETURN"
			if guardChain && strings.Contains(strings.Join(args, " "), "-j ACCEPT") {
				snapshot, e := outputContext(ctx, "iptables", "-S", chain)
				if e != nil {
					return e
				}
				position, terminal := 0, false
				for _, line := range strings.Split(snapshot, "\n") {
					if strings.HasPrefix(line, "-A "+chain+" ") {
						position++
						if strings.TrimSpace(line) == "-A "+chain+" -j DROP" {
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
			} else if guardChain && len(args) == 4 {
				args[check] = "-I" // Close the chain until the order check restores owned allowances.
			} else {
				position := 1
				target := args[len(args)-1]
				if missing.command == "iptables" && chain == "FORWARD" && (target == "S2L_FORWARD" || target == "S2L_RETURN") {
					if _, err := outputContext(ctx, "iptables", "-S", target); err != nil {
						if !IsMissingState(missingFirewall(err, "iptables", nil)) {
							return err
						}
						if err := runContext(ctx, "iptables", "-N", target); err != nil {
							return err
						}
						if err := runContext(ctx, "iptables", "-A", target, "-j", "DROP"); err != nil {
							return err
						}
					}
					if err := insertForwardHook(ctx, args[check+2:]...); err != nil {
						return err
					}
					continue
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

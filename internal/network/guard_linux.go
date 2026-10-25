//go:build linux

package network

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
)

// Match only the exact broad return rule emitted by older S2L versions. Reading
// kernel rules keeps guard-only independent of application config/state, which
// can be unavailable during failed upgrades. Never claim unrelated interfaces.
var legacyReturnRule = regexp.MustCompile(`^-A FORWARD -i ([A-Za-z0-9_.-]{1,15}) -m state --state (?:RELATED,ESTABLISHED|ESTABLISHED,RELATED) -m comment --comment (?:"s2l:[^"]*"|s2l:[^ ]+) -j ACCEPT$`)

// EnsureGuard must run before xl2tpd can accept clients. Guards intentionally
// survive teardown: an existing PPP session must never use the host's uplink.
// S2L requires a dedicated host; these rules cover every ppp interface.
func (c *Controller) EnsureGuard() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ensureGuard(c.management)
}

func ensureGuard(options ...model.ManagementAccess) error {
	for _, chain := range []string{"S2L_FORWARD", "S2L_RETURN"} {
		if err := run("iptables", "-N", chain); err != nil {
			if _, check := output("iptables", "-S", chain); check != nil {
				return err
			}
		}
		if err := ensureRule("iptables", "-A", chain, "-j", "DROP"); err != nil {
			return err
		}
	}
	for _, rule := range [][]string{
		{"FORWARD", "-i", "ppp+", "-j", "S2L_FORWARD"},
		{"INPUT", "-i", "ppp+", "-j", "DROP"},
	} {
		if err := ensureRule("iptables", "-I", rule...); err != nil {
			return err
		}
	}
	// The updater calls guard-only before stopping old PPP/TUN processes. Guard
	// their existing interfaces now, rather than waiting for mapping recreation.
	legacy, err := output("iptables", "-S", "FORWARD")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(legacy, "\n") {
		if match := legacyReturnRule.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			if err := ensureRule("iptables", "-I", "FORWARD", "-i", match[1], "-j", "S2L_RETURN"); err != nil {
				return err
			}
		}
	}
	// Fail startup if IPv6 is enabled but cannot be filtered. The installer
	// supplies ip6tables alongside iptables.
	for _, chain := range []string{"INPUT", "FORWARD"} {
		if err := ensureRule("ip6tables", "-I", chain, "-i", "ppp+", "-j", "DROP"); err != nil {
			return fmt.Errorf("IPv6 guard: %w", err)
		}
	}
	access := model.ManagementAccess{}
	if len(options) > 0 {
		access = options[0]
	}
	return ensureManagementAccess(access)
}

func ensureRule(bin, operation string, args ...string) error {
	check := append([]string{"-C"}, args...)
	if err := run(bin, check...); err == nil {
		return nil
	} else if !IsMissingState(missingFirewall(err, bin, check)) {
		return err
	}
	return run(bin, append([]string{operation}, args...)...)
}

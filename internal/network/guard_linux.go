//go:build linux

package network

import "fmt"

// EnsureGuard must run before xl2tpd can accept clients. Guards intentionally
// survive teardown: an existing PPP session must never use the host's uplink.
// S2L requires a dedicated host; these rules cover every ppp interface.
func (c *Controller) EnsureGuard() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ensureGuard()
}

func ensureGuard() error {
	if err := run("iptables", "-N", "S2L_FORWARD"); err != nil {
		if _, check := output("iptables", "-S", "S2L_FORWARD"); check != nil {
			return err
		}
	}
	if err := ensureRule("iptables", "-A", "S2L_FORWARD", "-j", "DROP"); err != nil {
		return err
	}
	for _, rule := range [][]string{
		{"FORWARD", "-i", "ppp+", "-j", "S2L_FORWARD"},
		{"INPUT", "-i", "ppp+", "-j", "DROP"},
	} {
		if err := ensureRule("iptables", "-I", rule...); err != nil {
			return err
		}
	}
	// Fail startup if IPv6 is enabled but cannot be filtered. The installer
	// supplies ip6tables alongside iptables.
	for _, chain := range []string{"INPUT", "FORWARD"} {
		if err := ensureRule("ip6tables", "-I", chain, "-i", "ppp+", "-j", "DROP"); err != nil {
			return fmt.Errorf("IPv6 guard: %w", err)
		}
	}
	return nil
}

func ensureRule(bin, operation string, args ...string) error {
	check := append([]string{"-C"}, args...)
	if run(bin, check...) == nil {
		return nil
	}
	return run(bin, append([]string{operation}, args...)...)
}

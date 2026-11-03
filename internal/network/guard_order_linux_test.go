//go:build linux

package network

import (
	"strings"
	"testing"
)

func TestGuardOrderPlanPreservesForeignRules(t *testing.T) {
	snapshot := `-P INPUT ACCEPT
-A INPUT -j ACCEPT
-A INPUT -i ppp+ -j DROP
-A INPUT -i ppp+ -j S2L_MANAGEMENT
-A FORWARD -j FOREIGN
-A FORWARD -i tun101 -j S2L_RETURN
-A FORWARD -i ppp+ -j S2L_FORWARD
-A FORWARD -i ppp+ -j S2L_FORWARD
-A FORWARD -i tun101 -m comment --comment foreign -j S2L_RETURN
`
	plan := guardOrderPlan(snapshot, false)
	if strings.Contains(plan, "FOREIGN") || strings.Contains(plan, "comment") || strings.Contains(plan, "-F ") || strings.Contains(plan, "-j ACCEPT") {
		t.Fatal("plan claimed foreign rules", plan)
	}
	if strings.Count(plan, "-D FORWARD -i ppp+ -j S2L_FORWARD") != 2 || strings.Count(plan, "-I FORWARD -i ppp+ -j S2L_FORWARD") != 1 {
		t.Fatal("duplicates not normalized", plan)
	}
	if strings.Index(plan, "-I INPUT -i ppp+ -j DROP") > strings.Index(plan, "-I INPUT -i ppp+ -j S2L_MANAGEMENT") {
		t.Fatal("management must be inserted last to remain first", plan)
	}
	// Once hooks occupy the prefix, foreign rules and policies need no changes.
	healthy := "-P INPUT DROP\n-A INPUT -i ppp+ -j S2L_MANAGEMENT\n-A INPUT -i ppp+ -j DROP\n-A INPUT -j ACCEPT\n-A FORWARD -i ppp+ -j S2L_FORWARD\n-A FORWARD -i tun101 -j S2L_RETURN\n-A FORWARD -j FOREIGN\n"
	if plan := guardOrderPlan(healthy, false); plan != "" {
		t.Fatal("healthy hooks were rewritten", plan)
	}
	if plan := guardOrderPlan("-A INPUT -j ACCEPT\n-A INPUT -i ppp+ -j DROP\n", true); !strings.Contains(plan, "-I INPUT -i ppp+ -j DROP") {
		t.Fatal("IPv6 shadowing missed", plan)
	}
}

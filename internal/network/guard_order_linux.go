//go:build linux

package network

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var returnHook = regexp.MustCompile(`^-A FORWARD -i [A-Za-z0-9_.-]{1,15} -j S2L_RETURN$`)

// guardOrderPlan promotes only exact S2L hooks. Never flush built-in chains or
// replay foreign rules. Restore commits each family's filter changes together,
// so moving INPUT hooks cannot temporarily remove PPP management access.
func guardOrderPlan(snapshot string, ipv6 bool) string {
	var plan strings.Builder
	for _, chain := range []string{"INPUT", "FORWARD"} {
		var rules, owned, desired []string
		seen := make(map[string]bool)
		for _, line := range strings.Split(snapshot, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "-A "+chain+" ") {
				continue
			}
			rules = append(rules, line)
			isOwned := line == "-A "+chain+" -i ppp+ -j DROP"
			if !ipv6 {
				if chain == "INPUT" {
					isOwned = isOwned || line == "-A INPUT -i ppp+ -j "+managementChain
				} else {
					isOwned = line == "-A FORWARD -i ppp+ -j S2L_FORWARD" || returnHook.MatchString(line)
				}
			}
			if !isOwned {
				continue
			}
			owned = append(owned, line)
			if !seen[line] {
				seen[line] = true
				if !ipv6 && line == "-A INPUT -i ppp+ -j "+managementChain {
					desired = append([]string{line}, desired...)
				} else {
					desired = append(desired, line)
				}
			}
		}
		correct := len(owned) == len(desired)
		for i, rule := range desired {
			correct = correct && rules[i] == rule
		}
		if correct {
			continue
		}
		for _, rule := range owned {
			plan.WriteString("-D" + rule[2:] + "\n")
		}
		for i := len(desired) - 1; i >= 0; i-- {
			plan.WriteString("-I" + desired[i][2:] + "\n")
		}
	}
	if plan.Len() == 0 {
		return ""
	}
	return "*filter\n" + plan.String() + "COMMIT\n"
}

func checkGuardOrder(ctx context.Context, repair bool) error {
	for _, bin := range []string{"iptables", "ip6tables"} {
		snapshot, err := outputContext(ctx, bin, "-S")
		if err != nil {
			return err // Inspection failures never authorize a mutation.
		}
		plan := guardOrderPlan(snapshot, bin == "ip6tables")
		if plan == "" {
			continue
		}
		if !repair {
			return missingState(bin+" PPP guard hooks are shadowed or duplicated", "guard-order")
		}
		cmd := exec.CommandContext(ctx, bin+"-restore", "-w", "5", "--noflush")
		cmd.Stdin = strings.NewReader(plan)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("restore %s guard order: %w: %s", bin, err, strings.TrimSpace(out.String()))
		}
	}
	return nil
}

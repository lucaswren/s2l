//go:build linux

package network

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"

	"github.com/lucaswren/s2l/internal/model"
	"strings"
)

var returnHook = regexp.MustCompile(`^-A FORWARD -i [A-Za-z0-9_.-]{1,15} -j S2L_RETURN$`)

// guardOrderPlan promotes only exact S2L hooks. Never flush built-in chains or
// replay foreign rules. Restore commits each family's filter changes together,
// so moving INPUT hooks cannot temporarily remove PPP management access.
func guardOrderPlan(snapshot string, ipv6 bool) string {
	var plan strings.Builder
	for _, chain := range []string{"INPUT", "FORWARD"} {
		var rules, owned, desired, gates, sites []string
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
			gate := !ipv6 && chain == "FORWARD" && isSessionGate(line)
			site := !ipv6 && chain == "FORWARD" && line == "-A FORWARD -j S2L-SITE-FORWARD"
			if !isOwned && !gate && !site {
				continue
			}
			owned = append(owned, line)
			if !seen[line] {
				seen[line] = true
				if site {
					sites = append(sites, line)
				} else if gate {
					gates = append(gates, line)
				} else if !ipv6 && line == "-A INPUT -i ppp+ -j "+managementChain {
					desired = append([]string{line}, desired...)
				} else {
					desired = append(desired, line)
				}
			}
		}
		desired = append(gates, desired...)
		desired = append(sites, desired...)
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

// iptables -S reorders match options and prints the implicit UDP module.
func mappingGuardRuleKey(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return line
	}
	var matches []string
	for i := 2; i < len(fields); {
		negate := ""
		if fields[i] == "!" {
			negate = "!"
			i++
		}
		if i+1 >= len(fields) {
			return line
		}
		option, value := fields[i], fields[i+1]
		i += 2
		if option == "-m" && (value == "udp" || value == "tcp") && negate == "" {
			continue
		}
		if option == "--comment" {
			value = strings.Trim(value, `"`)
		}
		if (option == "-s" || option == "-d") && !strings.Contains(value, "/") {
			value += "/32"
		}
		if option == "--state" || option == "--ctstate" {
			states := strings.Split(value, ",")
			sort.Strings(states)
			value = strings.Join(states, ",")
		}
		matches = append(matches, negate+option+" "+value)
	}
	sort.Strings(matches)
	return strings.Join(fields[:2], " ") + " " + strings.Join(matches, " ")
}

// Only exact rules belonging to active mappings may be moved. Leave foreign
// rules in place, behind the terminal DROP of these S2L-owned chains.
func mappingGuardOrderPlan(snapshot, chain string, mappings []model.TunnelMapping) string {
	terminal := "-A " + chain + " -j DROP"
	type ruleInfo struct {
		priority int
		mapping  string
	}
	expected := map[string]ruleInfo{terminal: {priority: 2}}
	for _, m := range mappings {
		if chain == "S2L_RETURN" {
			expected[mappingGuardRuleKey("-A "+chain+" "+strings.Join(mappingReturnArgs(m), " "))] = ruleInfo{priority: 1}
			continue
		}
		expected[mappingGuardRuleKey("-A "+chain+" "+strings.Join(mappingForwardArgs(m), " "))] = ruleInfo{priority: 1, mapping: mappingKey(m)}
		if args := mappingUDPRejectArgs(m); len(args) > 0 {
			expected[mappingGuardRuleKey("-A "+chain+" "+strings.Join(args, " "))] = ruleInfo{mapping: mappingKey(m)}
		}
	}
	var owned, desired []string
	seen := make(map[string]bool)
	accepted := make(map[string]bool)
	correct := true
	for _, line := range strings.Split(snapshot, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-A "+chain+" ") {
			continue
		}
		key := mappingGuardRuleKey(line)
		rule, found := expected[key]
		if !found {
			if !seen[terminal] {
				correct = false
			}
			continue
		}
		owned = append(owned, line)
		if seen[key] {
			correct = false
			continue
		}
		if seen[terminal] || (rule.priority == 0 && accepted[rule.mapping]) {
			correct = false
		}
		seen[key] = true
		if rule.priority == 1 {
			accepted[rule.mapping] = true
		}
		desired = append(desired, line)
	}
	if correct || !seen[terminal] {
		return ""
	}
	sort.SliceStable(desired, func(i, j int) bool {
		return expected[mappingGuardRuleKey(desired[i])].priority < expected[mappingGuardRuleKey(desired[j])].priority
	})
	var plan strings.Builder
	plan.WriteString("*filter\n")
	for _, line := range owned {
		plan.WriteString("-D" + line[2:] + "\n")
	}
	for i := len(desired) - 1; i >= 0; i-- {
		plan.WriteString("-I" + desired[i][2:] + "\n")
	}
	plan.WriteString("COMMIT\n")
	return plan.String()
}

func (c *Controller) checkMappingGuardOrder(ctx context.Context, m model.TunnelMapping, snapshot string, repair bool) error {
	mappings := make([]model.TunnelMapping, 0, len(c.active)+1)
	mappings = append(mappings, m)
	for key, active := range c.active {
		if key != mappingKey(m) {
			mappings = append(mappings, active)
		}
	}
	if repair {
		snapshot, err := outputContext(ctx, "iptables", "-S")
		if err != nil {
			return err
		}
		return c.applyMappingGuardOrder(ctx, mappings, snapshot, true)
	}
	return c.applyMappingGuardOrder(ctx, mappings, snapshot, false)
}

func (c *Controller) applyMappingGuardOrder(ctx context.Context, mappings []model.TunnelMapping, snapshot string, repair bool) error {
	for _, chain := range []string{"S2L_FORWARD", "S2L_RETURN"} {
		plan := mappingGuardOrderPlan(snapshot, chain, mappings)
		if plan == "" {
			continue
		}
		if !repair {
			return missingState(chain+" rules are shadowed or duplicated", "mapping-guard-order")
		}
		cmd := exec.CommandContext(ctx, "iptables-restore", "-w", "5", "--noflush")
		cmd.Stdin = strings.NewReader(plan)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("restore %s guard order: %w: %s", chain, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

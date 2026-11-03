//go:build linux

package network

import (
	"context"
	"strconv"
	"strings"
)

// Reserved on forwarded PPP packets. Clear it before the nftables hook can
// grant admission, so deleting that table leaves the independent filter closed.
const sessionMark = "0x40000000"
const sessionComment = "s2l:session-admission"

func sessionFirewallRules() [][]string {
	var rules [][]string
	for _, direction := range []string{"-i", "-o"} {
		rules = append(rules,
			[]string{"-t", "mangle", "-C", "FORWARD", direction, "ppp+", "-m", "comment", "--comment", sessionComment, "-j", "MARK", "--set-xmark", "0x0/" + sessionMark},
			[]string{"-C", "FORWARD", direction, "ppp+", "-m", "mark", "!", "--mark", sessionMark + "/" + sessionMark, "-m", "comment", "--comment", sessionComment, "-j", "DROP"})
	}
	return rules
}

func insertForwardHook(ctx context.Context, args ...string) error {
	snapshot, err := outputContext(ctx, "iptables", "-S", "FORWARD")
	if err != nil {
		return err
	}
	position := 1
	for _, line := range strings.Split(snapshot, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-A FORWARD ") {
			continue
		}
		if !isSessionGate(line) {
			break
		}
		position++
	}
	return runContext(ctx, "iptables", append([]string{"-I", "FORWARD", strconv.Itoa(position)}, args...)...)
}

func isSessionGate(line string) bool {
	line = strings.ReplaceAll(line, `"`+sessionComment+`"`, sessionComment)
	for _, rule := range sessionFirewallRules() {
		if rule[0] == "-C" && line == "-A "+strings.Join(rule[1:], " ") {
			return true
		}
	}
	return false
}

func ensureSessionFirewall() error {
	for _, rule := range sessionFirewallRules() {
		args := append([]string(nil), rule...)
		for i, arg := range args {
			if arg == "-C" {
				args[i] = "-I"
				break
			}
		}
		if err := run("iptables", rule...); err != nil {
			if !IsMissingState(missingFirewall(err, "iptables", rule)) {
				return err
			}
			if err := run("iptables", args...); err != nil {
				return err
			}
		}
	}
	return checkGuardOrder(context.Background(), true)
}

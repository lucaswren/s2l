//go:build linux

package network

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

// Exercise actual conntracked replies after the PPP route disappears. The WAN
// positive control proves this catches reverse-path leakage, even with a host
// firewall that otherwise accepts every forwarded packet.
func TestLeakGuardsIntegrationReturnTraffic(t *testing.T) {
	if os.Getenv("S2L_NETNS_TEST") != "1" {
		t.Skip("requires isolated root network namespace")
	}
	must := func(name string, args ...string) {
		t.Helper()
		if err := run(name, args...); err != nil {
			t.Fatal(err)
		}
	}
	client := fmt.Sprintf("s2l-return-client-%d", os.Getpid())
	upstream := fmt.Sprintf("s2l-return-tun-%d", os.Getpid())
	wan := fmt.Sprintf("s2l-return-wan-%d", os.Getpid())
	for _, link := range []struct{ host, ns, addr, peer string }{
		{"ppp91", client, "10.88.1.1/24", "10.88.1.2/24"},
		{"replytun", upstream, "198.19.1.1/24", "198.19.1.2/24"},
		{"replywan", wan, "198.19.2.1/24", "198.19.2.2/24"},
	} {
		must("ip", "netns", "add", link.ns)
		defer run("ip", "netns", "del", link.ns)
		must("ip", "link", "add", link.host, "type", "veth", "peer", "name", "replypeer")
		must("ip", "link", "set", "replypeer", "netns", link.ns)
		must("ip", "addr", "add", link.addr, "dev", link.host)
		must("ip", "link", "set", link.host, "up")
		must("ip", "netns", "exec", link.ns, "ip", "addr", "add", link.peer, "dev", "replypeer")
		must("ip", "netns", "exec", link.ns, "ip", "link", "set", "replypeer", "up")
		must("ip", "netns", "exec", link.ns, "ip", "link", "set", "lo", "up")
	}
	must("ip", "netns", "exec", client, "ip", "route", "add", "default", "via", "10.88.1.1")
	must("ip", "netns", "exec", upstream, "ip", "route", "add", "default", "via", "198.19.1.1")
	must("ip", "route", "add", "default", "via", "198.19.2.2", "metric", "32760")
	defer run("ip", "route", "del", "default", "via", "198.19.2.2", "metric", "32760")
	if err := ensureIPForward(); err != nil {
		t.Fatal(err)
	}
	controller := NewController()
	m := model.TunnelMapping{ID: "return-test", TunName: "replytun", TableID: 191, Subnet: "10.88.1.0/24", UDPPolicy: "allow"}
	// Simulate an older running installation before the upgrade's guard-only
	// call, so PPP shutdown is protected before mappings can be recreated.
	legacy := []string{"FORWARD", "-i", m.TunName, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT", "-m", "comment", "--comment", mappingComment(m)}
	must("iptables", append([]string{"-A"}, legacy...)...)
	if err := controller.EnsureGuard(); err != nil {
		t.Fatal(err)
	}
	must("iptables", "-C", "FORWARD", "-i", m.TunName, "-j", "S2L_RETURN")
	if err := AddPolicyRoute(m); err != nil {
		t.Fatal(err)
	}
	defer DelPolicyRoute(m)
	defer DelFirewall(m)
	// Verify upgrades remove the old broad allowance. Repeated setup
	// must retain exactly one persistent TUN hook and one return allowance.
	for i := 0; i < 2; i++ {
		if err := AddFirewall(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := run("iptables", append([]string{"-C"}, legacy...)...); err == nil {
		t.Fatal("legacy unrestricted TUN return rule survived upgrade")
	}
	rules, err := output("iptables", "-S", "FORWARD")
	if err != nil || strings.Count(rules, "-i replytun -j S2L_RETURN") != 1 {
		t.Fatalf("return hooks accumulated: %s %v", rules, err)
	}
	if err := addMappingTCPMSS(m); err != nil {
		t.Fatal(err)
	}
	// The same health repair used by running mappings must restore all three
	// return protections, with the allowance before the terminal DROP.
	for _, args := range [][]string{
		{"-D", "FORWARD", "-i", m.TunName, "-j", "S2L_RETURN"},
		{"-D", "S2L_RETURN", "-j", "DROP"},
		append([]string{"-D", "S2L_RETURN"}, mappingReturnArgs(m)...),
	} {
		must("iptables", args...)
		if !IsMissingState(controller.Verify(m)) {
			t.Fatal("missing return protection not detected", args)
		}
		if err := controller.RepairContext(context.Background(), m); err != nil {
			t.Fatal("return protection repair", err)
		}
	}

	server := exec.Command("ip", "netns", "exec", upstream, "python3", "-u", "-c", `
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(('198.19.1.2', 5000))
s.settimeout(10)
print('ready', flush=True)
data, peer = s.recvfrom(256)
s.sendto(data, peer)
print('connected', flush=True)
for line in sys.stdin:
    target = peer if line.strip() == 'reply' else ('10.88.1.2', 46001)
    s.sendto(b'late-reply', target)
    print('sent', flush=True)
`)
	in, err := server.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { in.Close(); server.Process.Kill(); server.Wait() }()
	reader := bufio.NewReader(out)
	waitLine := func(want string) {
		t.Helper()
		line := make(chan string, 1)
		go func() { value, _ := reader.ReadString('\n'); line <- strings.TrimSpace(value) }()
		select {
		case got := <-line:
			if got != want {
				t.Fatalf("UDP return fixture: got %q, want %q", got, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("UDP return fixture timed out", want)
		}
	}
	waitLine("ready")
	must("ip", "netns", "exec", client, "python3", "-c", `
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(('10.88.1.2', 46001))
s.settimeout(5)
s.sendto(b'fixed-return', ('198.19.1.2', 5000))
assert s.recv(256) == b'fixed-return'
`)
	waitLine("connected")
	must("iptables", "-t", "mangle", "-N", "S2L_RETURN_TEST")
	defer run("iptables", "-t", "mangle", "-X", "S2L_RETURN_TEST")
	must("iptables", "-t", "mangle", "-A", "S2L_RETURN_TEST", "-j", "RETURN")
	defer run("iptables", "-t", "mangle", "-F", "S2L_RETURN_TEST")
	must("iptables", "-t", "mangle", "-I", "POSTROUTING", "-o", "replywan", "-j", "S2L_RETURN_TEST")
	defer run("iptables", "-t", "mangle", "-D", "POSTROUTING", "-o", "replywan", "-j", "S2L_RETURN_TEST")
	count := func(table, chain, target string) int {
		t.Helper()
		text, err := output("iptables", "-t", table, "-nvxL", chain)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(text, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 2 && fields[2] == target {
				n, err := strconv.Atoi(fields[0])
				if err != nil {
					t.Fatal(err)
				}
				return n
			}
		}
		t.Fatal("counter missing", text)
		return -1
	}
	send := func(command string) {
		t.Helper()
		if _, err := fmt.Fprintln(in, command); err != nil {
			t.Fatal(err)
		}
		waitLine("sent")
	}
	// Remove only the client route, as pppd does on disconnect. Keep conntrack
	// and the userspace TUN alive so a real delayed reply is still generated.
	must("ip", "route", "del", m.Subnet, "dev", "ppp91")
	route, err := output("ip", "route", "get", "10.88.1.2", "from", "198.19.1.2", "iif", m.TunName)
	if err != nil || !strings.Contains(route, "dev replywan") {
		t.Fatalf("fixture has no default-route fallback: %s %v", route, err)
	}
	// Bypass the guard only in this isolated positive control. This reply must
	// be ESTABLISHED and hit the WAN counter, reproducing the old vulnerability.
	positive := []string{"S2L_RETURN", "-i", m.TunName, "-m", "state", "--state", "ESTABLISHED", "-j", "ACCEPT"}
	must("iptables", append([]string{"-I"}, positive...)...)
	send("reply")
	deadline := time.Now().Add(5 * time.Second)
	for count("mangle", "S2L_RETURN_TEST", "RETURN") == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if count("mangle", "S2L_RETURN_TEST", "RETURN") != 1 {
		t.Fatal("established late reply did not exercise WAN fallback")
	}
	must("iptables", append([]string{"-D"}, positive...)...)
	blocked := func(command string) {
		t.Helper()
		before := count("filter", "S2L_RETURN", "DROP")
		send(command)
		deadline := time.Now().Add(5 * time.Second)
		for count("filter", "S2L_RETURN", "DROP") == before && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		dropped, escaped := count("filter", "S2L_RETURN", "DROP"), count("mangle", "S2L_RETURN_TEST", "RETURN")
		if dropped != before+1 || escaped != 1 {
			t.Fatalf("late TUN reply: drop counter %d -> %d, WAN counter %d (want 1 positive control)", before, dropped, escaped)
		}
	}
	blocked("reply")
	// Reconnect remains functional; stopping the mapping must still drop any
	// replies queued by the TUN process before it exits.
	must("ip", "route", "add", m.Subnet, "dev", "ppp91")
	must("ip", "netns", "exec", client, "ping", "-c", "1", "-W", "3", "198.19.1.2")
	if err := DelFirewall(m); err != nil {
		t.Fatal(err)
	}
	must("ip", "route", "del", m.Subnet, "dev", "ppp91")
	// After NAT teardown, target the client directly so this still exercises
	// FORWARD even if the kernel retired the old masqueraded connection.
	blocked("direct")
}

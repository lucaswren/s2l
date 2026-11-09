//go:build linux

package network

import (
	"context"
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// CI runs this binary inside a fresh network namespace, never on host routes.
func TestLeakGuardsIntegration(t *testing.T) {
	if os.Getenv("S2L_NETNS_TEST") != "1" {
		t.Skip("requires isolated root network namespace")
	}
	must := func(name string, args ...string) {
		t.Helper()
		if err := run(name, args...); err != nil {
			t.Fatal(err)
		}
	}
	client := fmt.Sprintf("s2l-client-%d", os.Getpid())
	outside := fmt.Sprintf("s2l-outside-%d", os.Getpid())
	for _, ns := range []string{client, outside} {
		must("ip", "netns", "add", ns)
		defer run("ip", "netns", "del", ns)
	}
	for _, link := range []struct{ host, peer, ns, addr, peerAddr, v6, peerV6 string }{
		{"ppp0", "client0", client, "10.0.101.1/24", "10.0.101.2/24", "fd00:1::1/64", "fd00:1::2/64"},
		{"wan0", "outside0", outside, "198.18.0.1/24", "198.18.0.2/24", "fd00:2::1/64", "fd00:2::2/64"},
	} {
		must("ip", "link", "add", link.host, "type", "veth", "peer", "name", link.peer)
		must("ip", "link", "set", link.peer, "netns", link.ns)
		must("ip", "addr", "add", link.addr, "dev", link.host)
		must("ip", "-6", "addr", "add", link.v6, "dev", link.host, "nodad")
		must("ip", "link", "set", link.host, "up")
		for _, args := range [][]string{{"addr", "add", link.peerAddr, "dev", link.peer}, {"-6", "addr", "add", link.peerV6, "dev", link.peer, "nodad"}, {"link", "set", link.peer, "up"}, {"link", "set", "lo", "up"}} {
			must("ip", append([]string{"netns", "exec", link.ns, "ip"}, args...)...)
		}
	}
	must("sysctl", "-w", "net.ipv4.ip_forward=1")
	must("sysctl", "-w", "net.ipv6.conf.all.forwarding=1")
	must("ip", "netns", "exec", client, "ip", "route", "add", "default", "via", "10.0.101.1")
	must("ip", "netns", "exec", outside, "ip", "route", "add", "default", "via", "198.18.0.1")
	must("ip", "netns", "exec", client, "ip", "-6", "route", "add", "default", "via", "fd00:1::1")
	must("ip", "netns", "exec", outside, "ip", "-6", "route", "add", "default", "via", "fd00:2::1")
	ping := func(target string, success bool) {
		t.Helper()
		cmd := exec.Command("ip", "netns", "exec", client, "ping", "-c", "1", "-W", "3", target)
		out, err := cmd.CombinedOutput()
		if (err == nil) != success {
			t.Fatalf("ping %s success=%v: %v %s", target, success, err, out)
		}
	}
	must("iptables", "-t", "mangle", "-A", "POSTROUTING", "-s", "10.0.101.2", "-o", "wan0")
	wanPackets := func() int {
		t.Helper()
		counters, err := output("iptables", "-t", "mangle", "-nvxL", "POSTROUTING")
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(counters, "\n") {
			if strings.Contains(line, "10.0.101.2") && strings.Contains(line, "wan0") {
				n, err := strconv.Atoi(strings.Fields(line)[0])
				if err != nil {
					t.Fatal(err)
				}
				return n
			}
		}
		t.Fatal("WAN counter rule absent")
		return -1
	}
	ping("198.18.0.2", true)
	if wanPackets() == 0 {
		t.Fatal("WAN counter positive control saw no traffic")
	}
	ping("fd00:2::2", true)
	must("ip", "netns", "exec", client, "ip", "addr", "add", "10.0.102.2/32", "dev", "client0")
	must("ip", "route", "add", "10.0.102.2/32", "dev", "ppp0")
	spoof := func() *exec.Cmd {
		return exec.Command("ip", "netns", "exec", client, "ping", "-I", "10.0.102.2", "-c", "1", "-W", "2", "198.18.0.1")
	}
	if out, err := spoof().CombinedOutput(); err != nil {
		t.Fatalf("spoof fixture is not reachable before the guard: %v %s", err, out)
	}
	iface, err := net.InterfaceByName("ppp0")
	if err != nil {
		t.Fatal(err)
	}
	if err := NewController().EnableSessionGuard(); err != nil {
		t.Fatal(err)
	}
	ping("198.18.0.2", false)
	peers := map[string]model.PPPSession{"line": {Interface: "ppp0", PeerIP: "10.0.101.2", Index: iface.Index}}
	if err := replaceSessionGuard(context.Background(), peers); err != nil {
		t.Fatal(err)
	}
	ping("198.18.0.2", true)
	// A stale packet mark must not bypass admission after the nft table is lost.
	mark := []string{"-t", "mangle", "-A", "PREROUTING", "-i", "ppp0", "-j", "MARK", "--set-xmark", sessionMark + "/" + sessionMark}
	must("iptables", mark...)
	if err := removeSessionGuard(); err != nil {
		t.Fatal(err)
	}
	ping("198.18.0.2", false)
	mark[2] = "-D"
	must("iptables", mark...)
	if err := replaceSessionGuard(context.Background(), peers); err != nil {
		t.Fatal(err)
	}
	ping("198.18.0.2", true)
	if err := spoof().Run(); err == nil {
		t.Fatal("PPP session could send a packet with another mapping's source")
	}
	// Even a valid peer IP must not work under a different interface index.
	peers["line"] = model.PPPSession{Interface: "ppp0", PeerIP: "10.0.101.2", Index: iface.Index + 100}
	if err := replaceSessionGuard(context.Background(), peers); err != nil {
		t.Fatal(err)
	}
	ping("198.18.0.2", false)
	if err := removeSessionGuard(); err != nil {
		t.Fatal(err)
	}
	for _, rule := range sessionFirewallRules() {
		for i, arg := range rule {
			if arg == "-C" {
				rule[i] = "-D"
			}
		}
		must("iptables", rule...)
	}
	must("ip", "route", "del", "10.0.102.2/32", "dev", "ppp0")
	must("ip", "netns", "exec", client, "ip", "addr", "del", "10.0.102.2/32", "dev", "client0")
	if err := NewController().EnsureGuard(); err != nil {
		t.Fatal(err)
	}
	ping("198.18.0.2", false)
	ping("10.0.101.1", false)
	ping("fd00:2::2", false)
	ping("fd00:1::1", false)
	must("ip", "link", "add", "tun101", "type", "dummy")
	must("ip", "link", "set", "tun101", "up")
	m := model.TunnelMapping{ID: "test", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24"}
	if err := AddPolicyRoute(m); err != nil {
		t.Fatal(err)
	}
	if err := AddFirewall(m); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := addMappingTCPMSS(m); err != nil {
			t.Fatal(err)
		}
	}
	mssRules, err := output("iptables", "-t", "mangle", "-S", "FORWARD")
	if err != nil || strings.Count(mssRules, mappingComment(m)+"-mss") != 2 {
		t.Fatalf("MSS protection must cover both PPP directions once: %s %v", mssRules, err)
	}
	controller := NewController()
	if err := ensureIPForward(); err != nil {
		t.Fatal(err)
	}
	if err := TuneInterface(m.TunName, m.EffectiveMTU()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Verify(m); err != nil {
		t.Fatal("complete forwarding path rejected", err)
	}
	// Check that kernel drift is visible and repaired from the configured policy.
	for _, change := range [][]string{
		{"-D", "INPUT", "-i", "ppp+", "-j", managementChain},
		{"-I", managementChain, "1", "-j", "ACCEPT"},
	} {
		must("iptables", change...)
		if err := controller.Verify(m); !IsMissingState(err) {
			t.Fatal("PPP management drift not detected", err)
		}
		if err := controller.RepairContext(context.Background(), m); err != nil {
			t.Fatal("repair PPP management exceptions", err)
		}
		ping("198.18.0.1", false)
	}
	must("ip", "link", "set", "dev", m.TunName, "mtu", "1300", "txqueuelen", "10")
	if err := controller.Verify(m); !IsMissingState(err) {
		t.Fatal("TUN parameter drift not detected", err)
	}
	if err := controller.RepairContext(context.Background(), m); err != nil {
		t.Fatal("repair TUN parameters", err)
	}
	// Detect and repair rule order even when every individual rule exists.
	udpRule := mappingUDPRejectArgs(m)
	must("iptables", append([]string{"-D", "S2L_FORWARD"}, udpRule...)...)
	must("iptables", append([]string{"-A", "S2L_FORWARD"}, udpRule...)...)
	must("iptables", "-I", "S2L_RETURN", "1", "-j", "DROP")
	if err := controller.Verify(m); !IsMissingState(err) {
		t.Fatal("shadowed mapping rules not detected", err)
	}
	if err := controller.RepairContext(context.Background(), m); err != nil {
		t.Fatal("repair mapping chain order", err)
	}
	if err := controller.EnableSessionGuard(); err != nil {
		t.Fatal(err)
	}
	// Setup can encounter an admitted interface that no longer has its old
	// name. Revocation must reach the kernel before a no-op sync can skip writes.
	if err := controller.commitSessionGuard(context.Background(), map[string]model.PPPSession{
		m.ID: {Interface: "ppp-missing", PeerIP: "10.0.101.2", Index: iface.Index},
	}); err != nil {
		t.Fatal(err)
	}
	if err := controller.Setup(m); err != nil {
		t.Fatal(err)
	}
	admitted, err := output("nft", "-j", "list", "set", "ip", sessionTable, "peers")
	if err != nil || strings.Contains(admitted, "10.0.101.2") {
		t.Fatalf("setup left stale PPP admission in the kernel: %s %v", admitted, err)
	}
	// An idle reconciliation still checks for drift before skipping a write.
	must("nft", "flush", "chain", "ip", sessionTable, "input")
	if err := controller.SyncSessionGuard(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := controller.verifySessionGuard(context.Background()); err != nil {
		t.Fatal("idle sync did not repair admission drift", err)
	}
	if err := controller.commitSessionGuard(context.Background(), map[string]model.PPPSession{
		"fixture": {Interface: "ppp0", PeerIP: "10.0.101.2", Index: iface.Index},
	}); err != nil {
		t.Fatal(err)
	}
	for _, chain := range []string{"input", "forward"} {
		must("nft", "flush", "chain", "ip", sessionTable, chain)
		if err := controller.Verify(m); !IsMissingState(err) {
			t.Fatalf("empty %s admission chain not detected: %v", chain, err)
		}
		if err := controller.RepairContext(context.Background(), m); err != nil {
			t.Fatalf("repair %s admission chain: %v", chain, err)
		}
	}
	must("nft", "flush", "set", "ip", sessionTable, "peers")
	if err := controller.Verify(m); !IsMissingState(err) {
		t.Fatal("missing peer admission not detected", err)
	}
	if err := controller.RepairContext(context.Background(), m); err != nil {
		t.Fatal("repair admission set", err)
	}
	// This fixture uses veth PPP substitutes and does not pin real PPP returns.
	controller.admitted = nil
	controller.sessionGuard = false
	if err := removeSessionGuard(); err != nil {
		t.Fatal(err)
	}
	for _, rule := range sessionFirewallRules() {
		for i, arg := range rule {
			if arg == "-C" {
				rule[i] = "-D"
			}
		}
		must("iptables", rule...)
	}
	// A firewall reload can insert broad accepts above still-existing hooks.
	// Presence-only checks used to report this leaking state as healthy.
	for _, bin := range []string{"iptables", "ip6tables"} {
		for _, chain := range []string{"INPUT", "FORWARD"} {
			must(bin, "-I", chain, "1", "-j", "ACCEPT")
		}
	}
	// Use a host address outside the client subnet: replies sourced from the
	// PPP gateway itself match the mapping's source policy and enter tun101.
	ping("198.18.0.1", true)
	ping("fd00:2::2", true)
	before, err := output("iptables", "-S")
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Verify(m); !IsMissingState(err) {
		t.Fatal("shadowed hooks not detected", err)
	}
	after, err := output("iptables", "-S")
	if err != nil || before != after {
		t.Fatal("verification mutated rules", err)
	}
	if err := controller.RepairContext(context.Background(), m); err != nil {
		t.Fatal("guard order repair", err)
	}
	for _, bin := range []string{"iptables", "ip6tables"} {
		for _, chain := range []string{"INPUT", "FORWARD"} {
			must(bin, "-C", chain, "-j", "ACCEPT")
		}
	}
	ping("198.18.0.1", false)
	ping("fd00:2::2", false)
	ping("fd00:1::1", false)
	// Existing mappings upgraded without the kernel rejection rule must be
	// detected as incomplete, so the normal repair installs it automatically.
	udpArgs := mappingUDPRejectArgs(m)
	must("iptables", append([]string{"-D", "S2L_FORWARD"}, udpArgs...)...)
	if controller.Verify(m) == nil {
		t.Fatal("missing UDP rejection rule not detected")
	}
	if err := controller.RepairContext(context.Background(), m); err != nil {
		t.Fatal("UDP repair", err)
	}
	must("iptables", mappingMSSDeleteArgs(m, "-i")...)
	if controller.Verify(m) == nil {
		t.Fatal("missing MSS rule not detected")
	}
	if err := controller.RepairContext(context.Background(), m); err != nil {
		t.Fatal("MSS repair", err)
	}
	must("ip", "link", "set", "dev", m.TunName, "down")
	if err := controller.Verify(m); !IsMissingState(err) {
		t.Fatal("TUN down was not confirmed", err)
	}
	if err := controller.RepairContext(context.Background(), m); err != nil {
		t.Fatal("TUN up repair", err)
	}
	must("iptables", "-D", "S2L_FORWARD", "-i", "ppp+", "-s", m.Subnet, "-o", m.TunName, "-j", "ACCEPT", "-m", "comment", "--comment", mappingComment(m))
	if err := controller.RepairContext(context.Background(), m); err != nil {
		t.Fatal("allow repair", err)
	}
	ordered, e := output("iptables", "-S", "S2L_FORWARD")
	if e != nil || strings.Index(ordered, "-j REJECT") > strings.Index(ordered, "-j ACCEPT") {
		t.Fatal("repair bypassed UDP policy", ordered, e)
	}
	must("ip", "route", "del", "unreachable", "default", "table", strconv.Itoa(m.TableID), "metric", "32767")
	if err := controller.RepairContext(context.Background(), m); err != nil {
		t.Fatal("fallback repair", err)
	}
	// A firewall/network reload can erase the whole FIB while TUN remains
	// alive. Missing-state repair must recreate it without a mapping restart.
	must("ip", "route", "flush", "table", strconv.Itoa(m.TableID))
	if err := controller.Verify(m); !IsMissingState(err) {
		t.Fatal("absent policy table did not authorize owned-state repair", err)
	}
	if err := controller.RepairContext(context.Background(), m); err != nil {
		t.Fatal("empty policy table repair", err)
	}
	if err := controller.Verify(m); err != nil {
		t.Fatal("repaired policy table rejected", err)
	}
	route, err := output("ip", "route", "get", "198.18.0.2", "from", "10.0.101.2", "iif", "ppp0")
	if err != nil || !strings.Contains(route, "dev tun101") {
		t.Fatalf("healthy routing: %s %v", route, err)
	}
	ping("198.18.0.2", false)
	counters, err := output("iptables", "-nvxL", "S2L_FORWARD")
	allowed := false
	for _, line := range strings.Split(counters, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 2 && fields[2] == "ACCEPT" {
			n, _ := strconv.Atoi(fields[0])
			allowed = n > 0
		}
	}
	if err != nil || !allowed {
		t.Fatalf("configured TUN forwarding never reached allowance: %s %v", counters, err)
	}
	// Resource conflict refusal must not erase another application's routes.
	foreign := model.TunnelMapping{ID: "foreign", TunName: "tun202", TableID: 202, Subnet: "10.66.2.0/24"}
	must("ip", "route", "add", "10.5.0.0/24", "dev", "wan0", "table", "202")
	if DelPolicyRoute(foreign) == nil || AddPolicyRoute(foreign) == nil {
		t.Fatal("foreign table was accepted")
	}
	saved, e := output("ip", "-j", "route", "show", "table", "202")
	if e != nil || !strings.Contains(saved, "10.5.0.0/24") {
		t.Fatal("foreign route was destroyed", saved, e)
	}
	must("ip", "route", "del", "10.5.0.0/24", "dev", "wan0", "table", "202")
	must("ip", "link", "add", "tun202", "type", "dummy")
	if CheckResources(foreign, false) == nil {
		t.Fatal("foreign interface was accepted")
	}
	must("ip", "link", "del", "tun202")
	must("ip", "addr", "add", "10.66.2.1/24", "dev", "wan0")
	if CheckResources(foreign, false) == nil {
		t.Fatal("host subnet collision was accepted")
	}
	must("ip", "addr", "del", "10.66.2.1/24", "dev", "wan0")
	// Kill the interface: terminal unreachable must prevent main-table fallback.
	must("iptables", "-t", "mangle", "-Z", "POSTROUTING")
	must("ip", "link", "del", "tun101")
	if _, err := output("ip", "route", "get", "198.18.0.2", "from", "10.0.101.2", "iif", "ppp0"); err == nil {
		t.Fatal("TUN deletion allowed route fallback")
	}
	ping("198.18.0.2", false)
	// Even if the route table is accidentally flushed, the firewall must hold.
	must("ip", "route", "flush", "table", "101")
	ping("198.18.0.2", false)
	if err := DelFirewall(m); err != nil {
		t.Fatal(err)
	}
	mssRules, err = output("iptables", "-t", "mangle", "-S", "FORWARD")
	if err != nil || strings.Contains(mssRules, mappingComment(m)+"-mss") {
		t.Fatalf("MSS cleanup failed: %s %v", mssRules, err)
	}
	if err := DelPolicyRoute(m); err != nil {
		t.Fatal(err)
	}
	ping("198.18.0.2", false)
	must("ip", "netns", "exec", client, "ip", "addr", "add", "10.99.0.2/24", "dev", "client0")
	if err := run("ip", "netns", "exec", client, "ping", "-I", "10.99.0.2", "-c", "1", "-W", "1", "198.18.0.2"); err == nil {
		t.Fatal("spoofed source bypassed guard")
	}
	if leaked := wanPackets(); leaked != 0 {
		t.Fatalf("WAN leak: %d packets", leaked)
	}
	// Host upstream access is unaffected.
	must("ping", "-c", "1", "-W", "1", "198.18.0.2")
}

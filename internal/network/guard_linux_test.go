//go:build linux

package network

import (
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
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
	ping("198.18.0.2", true)
	ping("fd00:2::2", true)
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
	if err := controller.Verify(m); err != nil {
		t.Fatal("complete forwarding path rejected", err)
	}
	// Existing mappings upgraded without the kernel rejection rule must be
	// detected as incomplete, so the normal repair installs it automatically.
	udpArgs := mappingUDPRejectArgs(m)
	must("iptables", append([]string{"-D", "S2L_FORWARD"}, udpArgs...)...)
	if controller.Verify(m) == nil {
		t.Fatal("missing UDP rejection rule not detected")
	}
	must("iptables", append([]string{"-I", "S2L_FORWARD", "1"}, udpArgs...)...)
	must("iptables", mappingMSSDeleteArgs(m, "-i")...)
	if controller.Verify(m) == nil {
		t.Fatal("missing MSS rule not detected")
	}
	if err := addMappingTCPMSS(m); err != nil {
		t.Fatal(err)
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
	// Kill the interface: terminal unreachable must prevent main-table fallback.
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
	// Host upstream access is unaffected.
	must("ping", "-c", "1", "-W", "1", "198.18.0.2")
}

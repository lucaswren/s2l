//go:build linux

package network

import (
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPPPManagementDialHelper(t *testing.T) {
	target := os.Getenv("S2L_MANAGEMENT_DIAL")
	if target == "" {
		t.Skip("helper only")
	}
	d := net.Dialer{Timeout: 500 * time.Millisecond}
	if source := os.Getenv("S2L_MANAGEMENT_SOURCE"); source != "" {
		d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(source)}
	}
	c, e := d.Dial("tcp4", target)
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
}
func TestManagementRuleScope(t *testing.T) {
	a := model.ManagementAccess{Enabled: true, DestinationIP: "156.245.235.230", SourceCIDRs: []string{"10.0.101.0/24"}, TCPPorts: []int{38866, 51234}}
	text := managementRules(a, true)
	if strings.Contains(text, "FORWARD") || !strings.Contains(text, "-s 10.0.101.0/24 -d 156.245.235.230 -p tcp --dport 51234") || strings.Count(text, "-j ACCEPT") != 2 || !strings.Contains(text, "-j DROP") {
		t.Fatal(text)
	}
	text = managementRules(model.ManagementAccess{}, true)
	if strings.Contains(text, "ACCEPT") {
		t.Fatal(text)
	}
}
func TestPPPManagementIntegration(t *testing.T) {
	if os.Getenv("S2L_NETNS_TEST") != "1" {
		t.Skip("requires isolated Linux root namespace")
	}
	must := func(name string, args ...string) {
		t.Helper()
		if e := run(name, args...); e != nil {
			t.Fatal(e)
		}
	}
	ns := fmt.Sprintf("s2l-management-%d", os.Getpid())
	must("ip", "netns", "add", ns)
	defer run("ip", "netns", "del", ns)
	must("ip", "link", "set", "lo", "up")
	must("ip", "addr", "add", "198.18.10.1/32", "dev", "lo")
	defer run("ip", "addr", "del", "198.18.10.1/32", "dev", "lo")
	must("ip", "addr", "add", "198.18.10.2/32", "dev", "lo")
	defer run("ip", "addr", "del", "198.18.10.2/32", "dev", "lo")
	must("ip", "link", "add", "ppp99", "type", "veth", "peer", "name", "client99")
	defer run("ip", "link", "del", "ppp99")
	must("ip", "link", "set", "client99", "netns", ns)
	must("ip", "addr", "add", "10.20.30.1/24", "dev", "ppp99")
	must("ip", "link", "set", "ppp99", "up")
	must("ip", "netns", "exec", ns, "ip", "addr", "add", "10.20.30.2/24", "dev", "client99")
	must("ip", "netns", "exec", ns, "ip", "addr", "add", "10.20.31.2/24", "dev", "client99")
	must("ip", "netns", "exec", ns, "ip", "link", "set", "client99", "up")
	must("ip", "netns", "exec", ns, "ip", "route", "add", "default", "via", "10.20.30.1")
	must("ip", "route", "add", "10.20.31.0/24", "via", "10.20.30.2", "dev", "ppp99")
	one, e := net.Listen("tcp4", "0.0.0.0:0")
	if e != nil {
		t.Fatal(e)
	}
	defer one.Close()
	two, e := net.Listen("tcp4", "0.0.0.0:0")
	if e != nil {
		t.Fatal(e)
	}
	defer two.Close()
	port := one.Addr().(*net.TCPAddr).Port
	other := two.Addr().(*net.TCPAddr).Port
	dial := func(ip string, p int, source string, ok bool) {
		t.Helper()
		cmd := exec.Command("ip", "netns", "exec", ns, os.Args[0], "-test.run", "^TestPPPManagementDialHelper$")
		cmd.Env = append(os.Environ(), fmt.Sprintf("S2L_MANAGEMENT_DIAL=%s:%d", ip, p), "S2L_MANAGEMENT_SOURCE="+source)
		out, e := cmd.CombinedOutput()
		if (e == nil) != ok {
			t.Fatalf("dial %s:%d source=%s expected=%v: %v %s", ip, p, source, ok, e, out)
		}
	}
	c := NewController()
	if e := c.EnsureGuard(); e != nil {
		t.Fatal(e)
	}
	dial("198.18.10.1", port, "", false)
	a := model.ManagementAccess{Enabled: true, DestinationIP: "198.18.10.1", SourceCIDRs: []string{"10.20.30.0/24"}, TCPPorts: []int{port}}
	if e := c.SetManagementAccess(a); e != nil {
		t.Fatal(e)
	}
	dial("198.18.10.1", port, "", true)
	dial("198.18.10.1", other, "", false)
	dial("198.18.10.2", port, "", false)
	dial("198.18.10.1", port, "10.20.31.2", false)
	if e := c.EnsureGuard(); e != nil {
		t.Fatal(e)
	}
	dial("198.18.10.1", port, "", true)
	if e := c.SetManagementAccess(model.ManagementAccess{}); e != nil {
		t.Fatal(e)
	}
	dial("198.18.10.1", port, "", false)
	local, e := net.DialTimeout("tcp4", fmt.Sprintf("198.18.10.1:%d", port), time.Second)
	if e != nil {
		t.Fatal("non-PPP management affected", e)
	}
	local.Close()
}

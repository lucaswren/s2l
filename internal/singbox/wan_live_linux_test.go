//go:build linux

package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/speedtest"
)

// The selected WAN and the default route lead to different servers at the same
// destination. This catches both accidental default-route use and failover.
func TestLiveWANInterfaceIsolation(t *testing.T) {
	if os.Getenv("S2L_WAN_INTEGRATION") != "1" {
		t.Skip("requires disposable network/mount namespaces")
	}
	a, _ := os.Readlink("/proc/self/ns/net")
	b, _ := os.Readlink("/proc/1/ns/net")
	if a == b || a == "" || b == "" {
		t.Fatal("refusing host network namespace")
	}
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	run("ip", "link", "set", "lo", "up")
	run("sysctl", "-w", "net.ipv4.conf.all.rp_filter=0")
	// Both exits use the same remote address. The reverse path via the
	// default-route fixture must not filter replies on the selected WAN.
	run("sysctl", "-w", "net.ipv4.conf.default.rp_filter=0")
	dnsDir := t.TempDir()
	for _, pair := range []struct{ dev, ns, label string }{{"wanfixture", "s2l-wan-live", "WAN"}, {"fallbackfixture", "s2l-fallback-live", "FALLBACK"}} {
		run("ip", "netns", "add", pair.ns)
		t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", pair.ns).Run() })
		run("ip", "link", "add", pair.dev, "type", "veth", "peer", "name", "peer0", "netns", pair.ns)
		run("ip", "addr", "add", "198.18.0.1/24", "dev", pair.dev)
		run("ip", "link", "set", pair.dev, "up")
		run("ip", "netns", "exec", pair.ns, "ip", "link", "set", "lo", "up")
		run("ip", "netns", "exec", pair.ns, "ip", "addr", "add", "198.18.0.2/24", "dev", "peer0")
		run("ip", "netns", "exec", pair.ns, "ip", "link", "set", "peer0", "up")
		server := exec.Command("ip", "netns", "exec", pair.ns, "python3", "-u", "-c", `import socket,sys
s=socket.socket();s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1);s.bind(('198.18.0.2',4080));s.listen()
while True:
 c,_=s.accept();c.sendall(sys.argv[1].encode());c.close()`, pair.label)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = server.Process.Kill(); _ = server.Wait() })

		run("ip", "netns", "exec", pair.ns, "ip", "addr", "add", "1.1.1.1/32", "dev", "lo")
		dnsServer := exec.Command("ip", "netns", "exec", pair.ns, "python3", "-u", "-c", `import socket,struct,sys
s=socket.socket();s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1);s.bind(('1.1.1.1',53));s.listen()
def exact(c,n):
 b=b''
 while len(b)<n:
  part=c.recv(n-len(b))
  if not part: raise EOFError()
  b+=part
 return b
while True:
 c,_=s.accept()
 try:
  q=exact(c,struct.unpack('!H',exact(c,2))[0])
  end=12
  while q[end]: end+=q[end]+1
  end+=5
  question=q[12:end]
  response=q[:2]+b'\x81\x80'+struct.pack('!HHHH',1,1,0,0)+question+b'\xc0\x0c'+struct.pack('!HHIH',1,1,30,4)+socket.inet_aton('198.18.0.2')
  with open(sys.argv[1],'a') as f: f.write('query\n')
  c.sendall(struct.pack('!H',len(response))+response)
 except (OSError,EOFError): pass
 finally: c.close()`, filepath.Join(dnsDir, pair.label))
		if err := dnsServer.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = dnsServer.Process.Kill(); _ = dnsServer.Wait() })
	}
	run("ip", "route", "replace", "198.18.0.2/32", "dev", "fallbackfixture")
	run("ip", "route", "replace", "default", "via", "198.18.0.2", "dev", "wanfixture")
	run("ip", "route", "replace", "1.1.1.1/32", "via", "198.18.0.2", "dev", "fallbackfixture")
	read := func(c net.Conn, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		b, e := io.ReadAll(c)
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	var defaultReady bool
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		c, err := net.DialTimeout("tcp4", "198.18.0.2:4080", time.Second)
		if err == nil {
			if read(c, nil) != "FALLBACK" {
				t.Fatal("invalid default route fixture")
			}
			defaultReady = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !defaultReady {
		t.Fatal("default exit server not ready")
	}
	node := model.SocksNode{Egress: "wan", WANInterface: "wanfixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if got := read(speedtest.DialNode(ctx, node, "198.18.0.2:4080")); got != "WAN" {
		t.Fatal("WAN DNS/probe dialer used", got)
	}
	// No UDP DNS listener exists. Default-route DNS reaches the other namespace.
	if got := read(speedtest.DialNode(ctx, node, "wan.fixture.invalid:4080")); got != "WAN" {
		t.Fatal("WAN hostname dial used", got)
	}
	if data, err := os.ReadFile(filepath.Join(dnsDir, "WAN")); err != nil || len(data) == 0 {
		t.Fatal("selected WAN did not carry DNS bootstrap", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dnsDir, "FALLBACK")); len(data) != 0 {
		t.Fatal("DNS bootstrap used default exit")
	}
	mapping := model.TunnelMapping{TunName: "tun101", TableID: 101}
	data, err := GenerateConfig(node, mapping)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(data, &cfg)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	cfg["inbounds"] = []map[string]any{{"type": "socks", "listen": "127.0.0.1", "listen_port": port}}
	data, _ = json.Marshal(cfg)
	path := filepath.Join(t.TempDir(), "wan.json")
	_ = os.WriteFile(path, data, 0600)
	bin := os.Getenv("S2L_SINGBOX_CHECK_BIN")
	if bin == "" {
		t.Fatal("missing sing-box")
	}
	if out, err := exec.Command(bin, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("pinned runtime: %v %s", err, out)
	}
	cmd := exec.Command(bin, "run", "-c", path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	address := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
	var ready bool
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		c, err := speedtest.DialSOCKS5(ctx, address, "", "", "198.18.0.2:4080")
		if err == nil {
			if read(c, nil) != "WAN" {
				t.Fatal("sing-box used wrong egress")
			}
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatal("WAN outbound not ready")
	}
	run("ip", "link", "set", "wanfixture", "down")
	if c, err := speedtest.DialNode(ctx, node, "198.18.0.2:4080"); err == nil {
		c.Close()
		t.Fatal("WAN dialer fell back")
	}
	if c, err := speedtest.DialSOCKS5(ctx, address, "", "", "198.18.0.2:4080"); err == nil {
		c.Close()
		t.Fatal("WAN outbound fell back")
	}
	if got := read(net.DialTimeout("tcp4", "198.18.0.2:4080", time.Second)); !strings.EqualFold(got, "FALLBACK") {
		t.Fatal("default server failed during outage")
	}
	t.Log("selected WAN used by application and DNS/probe dialers; WAN loss cannot use the available default exit")
}

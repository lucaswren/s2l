//go:build linux

package service

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/network"
	"github.com/lucaswren/s2l/internal/singbox"
	"github.com/lucaswren/s2l/internal/store"
)

// Two WAN interfaces serve identical targets with different exit markers. The
// ordinary default route serves a third marker, catching accidental fallback.
func integrationWANExit(t *testing.T, root string, cert tls.Certificate, caPath string, start func(string, ...string), i int) model.SocksNode {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("WAN fixture %v: %v %s", args, err, out)
		}
	}
	ns, dev := fmt.Sprintf("s2l-wan-%d", i), fmt.Sprintf("wanfixture%d", i)
	local, peer := fmt.Sprintf("198.19.%d.1", i+10), fmt.Sprintf("198.19.%d.2", i+10)
	run("ip", "netns", "add", ns)
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", ns).Run() })
	run("ip", "link", "add", dev, "type", "veth", "peer", "name", "eth0", "netns", ns)
	run("ip", "addr", "add", local+"/30", "dev", dev)
	run("ip", "link", "set", dev, "up")
	run("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up")
	run("ip", "netns", "exec", ns, "ip", "addr", "add", peer+"/30", "dev", "eth0")
	run("ip", "netns", "exec", ns, "ip", "link", "set", "eth0", "up")
	for _, ip := range []string{"198.18.0.9", "8.8.8.8", "1.1.1.1"} {
		run("ip", "netns", "exec", ns, "ip", "addr", "add", ip+"/32", "dev", "lo")
	}
	run("ip", "netns", "exec", ns, "ip", "route", "add", "default", "via", local)
	run("ip", "route", "add", "default", "via", peer, "dev", dev, "metric", fmt.Sprint(100+i))
	// Replies on a bound non-default WAN are intentionally asymmetric to main.
	run("sysctl", "-qw", "net.ipv4.conf.all.rp_filter=0")
	run("sysctl", "-qw", "net.ipv4.conf."+dev+".rp_filter=0")
	keyPath := filepath.Join(root, "wan-key.pem")
	key := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(cert.PrivateKey.(*rsa.PrivateKey))})
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		t.Fatal(err)
	}
	start("ip", "netns", "exec", ns, "python3", "-u", "-c", `import socket,struct,threading,http.server,socketserver,ssl,sys
label=sys.argv[3].encode()
def answer(q):
 q=bytearray(q);q[2:4]=b'\x81\x80';q[6:12]=b'\x00\x01\x00\x00\x00\x00'
 if q[-4:-2]==b'\x00\x41':return bytes(q)+b'\xc0\x0c\x00\x41\x00\x01\x00\x00\x00\x3c\x00\x0d\x00\x01\x00\x00\x01\x00\x06\x02h2\x02h3'
 return bytes(q)+b'\xc0\x0c\x00\x01\x00\x01\x00\x00\x00\x3c\x00\x04'+socket.inet_aton('198.18.0.9')
def exact(c,n):
 b=b''
 while len(b)<n:
  a=c.recv(n-len(b))
  if not a:raise EOFError()
  b+=a
 return b
def dns_client(c):
 try:
  with c:
   c.settimeout(10)
   while True:
    q=exact(c,struct.unpack('!H',exact(c,2))[0]);a=answer(q);c.sendall(struct.pack('!H',len(a))+a)
 except (OSError,EOFError):pass
def dns():
 s=socket.socket();s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1);s.bind(('8.8.8.8',53));s.listen()
 while True:threading.Thread(target=dns_client,args=(s.accept()[0],),daemon=True).start()
def udp(port):
 s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.bind(('198.18.0.9',port))
 while True:
  q,peer=s.recvfrom(65535);s.sendto(label+b':'+q,peer)
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  body=label
  if self.path=='/bandwidth-test':
   self.send_response(200);self.send_header('X-Fixture-Exit',label.decode());self.send_header('Content-Length',str(64*1024*1024));self.end_headers()
   try:
    for i in range(1024):self.wfile.write(bytes(65536))
   except OSError:pass
   return
  self.send_response(200);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
 def do_POST(self):
  q=self.rfile.read(int(self.headers.get('Content-Length','0')));body=answer(q)
  self.send_response(200);self.send_header('Content-Type','application/dns-message');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
 def log_message(self,*a):pass
class Server(http.server.ThreadingHTTPServer):
 def server_bind(self):
  socketserver.TCPServer.server_bind(self);self.server_name,self.server_port=self.server_address[:2]
for f,args in [(dns,()),(udp,(9999,)),(udp,(443,))]:threading.Thread(target=f,args=args,daemon=True).start()
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain(sys.argv[1],sys.argv[2])
servers=[]
for ip in ['198.18.0.9','1.1.1.1']:
 s=Server((ip,443),Handler);s.socket=ctx.wrap_socket(s.socket,server_side=True);threading.Thread(target=s.serve_forever,daemon=True).start();servers.append(s)
threading.Event().wait()
`, caPath, keyPath, fmt.Sprintf("node%d", i))
	return model.SocksNode{ID: fmt.Sprintf("node%d", i), Egress: "wan", WANInterface: dev}
}

func integrationWANLifecycle(t *testing.T, svc *MappingService, st *store.Store, sb *singbox.Manager, caPath string, run func(...string) string, noFallback func(), stopGuard context.CancelFunc, guardDone <-chan struct{}) {
	t.Helper()
	request := func(i int) *exec.Cmd {
		return exec.Command("ip", "netns", "exec", fmt.Sprintf("s2l-dial-%d", i), "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "2", "--cacert", caPath, "https://198.18.0.9/")
	}
	healthy := func(i int) {
		t.Helper()
		integrationWait(t, 10*time.Second, func() bool {
			out, err := request(i).Output()
			return err == nil && strings.TrimSpace(string(out)) == fmt.Sprintf("node%d", i)
		})
	}
	offline := func(i int) {
		t.Helper()
		if out, err := request(i).CombinedOutput(); err == nil {
			t.Fatalf("WAN fault bypassed isolation: %s", out)
		}
	}
	for i := 0; i < 2; i++ {
		healthy(i)
		integrationClientUDP(t, fmt.Sprintf("s2l-dial-%d", i), fmt.Sprintf("node%d", i), "allow", true)
	}
	// Force TCP DNS failure. DoH must remain on the same selected interface.
	run("ip", "netns", "exec", "s2l-wan-1", "iptables", "-I", "INPUT", "-p", "tcp", "--dport", "53", "-j", "REJECT", "--reject-with", "tcp-reset")
	for i, ip := range model.ClientDNSAddresses() {
		integrationClientDNS(t, "s2l-dial-1", fmt.Sprintf("wan-fallback%d.test", i), ip, true)
	}
	if sb.ResolverStatus("tun102").Active != "doh" {
		t.Fatal("WAN DNS did not fail over to DoH")
	}
	run("ip", "netns", "exec", "s2l-wan-1", "iptables", "-D", "INPUT", "-p", "tcp", "--dport", "53", "-j", "REJECT", "--reject-with", "tcp-reset")
	run("ip", "netns", "exec", "s2l-wan-1", "iptables", "-I", "INPUT", "-d", "1.1.1.1", "-p", "tcp", "--dport", "443", "-j", "REJECT", "--reject-with", "tcp-reset")
	integrationClientDNS(t, "s2l-dial-1", "wan-recovery.test", model.ClientDNSAddresses()[0], true)
	if sb.ResolverStatus("tun102").Active != "tcp" {
		t.Fatal("WAN TCP DNS did not recover")
	}
	run("ip", "netns", "exec", "s2l-wan-1", "iptables", "-D", "INPUT", "-d", "1.1.1.1", "-p", "tcp", "--dport", "443", "-j", "REJECT", "--reject-with", "tcp-reset")
	noFallback()
	// A late kernel TCP reset can use the main routing table after the
	// device-bound socket closes. Exercise that output path explicitly.
	if route := run("ip", "route", "get", "8.8.8.8", "from", "198.19.11.1"); !strings.Contains(route, "dev fixture-wan ") {
		t.Fatal("WAN reset fixture needs a competing default route", route)
	}
	run("python3", "-c", `import errno,socket,struct
src,dst='198.19.11.1','8.8.8.8'
tcp=struct.pack('!HHIIBBHHH',45001,53,1,0,5<<4,4,0,0,0)
pseudo=socket.inet_aton(src)+socket.inet_aton(dst)+struct.pack('!BBH',0,socket.IPPROTO_TCP,len(tcp))
total=sum(struct.unpack('!16H',pseudo+tcp))
while total>>16:total=(total&65535)+(total>>16)
tcp=tcp[:16]+struct.pack('!H',(~total)&65535)+tcp[18:]
with socket.socket(socket.AF_INET,socket.SOCK_RAW,socket.IPPROTO_TCP) as s:
 s.bind((src,0))
 try:s.sendto(tcp,(dst,0))
 except OSError as e:
  if e.errno not in (errno.EPERM,errno.EACCES):raise
`)
	noFallback()
	t.Log("late WAN-source TCP reset did not leave through the competing default interface")
	// Selected WAN loss must not use the reachable default route or the other WAN.
	run("ip", "link", "set", "wanfixture0", "down")
	offline(0)
	healthy(1)
	noFallback()
	run("ip", "link", "set", "wanfixture0", "up")
	healthy(0)
	// A dead TUN must also fail closed while the other account stays usable.
	if err := sb.Stop("tun101"); err != nil {
		t.Fatal(err)
	}
	offline(0)
	healthy(1)
	noFallback()
	m, _ := st.GetMapping("m0")
	if err := svc.Start(m.ID); err != nil {
		t.Fatal(err)
	}
	healthy(0)
	// Disable reconciliation to prove Stop itself removes admission immediately.
	admitted, err := network.ReadPeers(st.ListMappings())
	if err != nil || len(admitted) != 2 {
		t.Fatal("missing admission positive control", admitted, err)
	}
	for _, p := range admitted {
		run("nft", "get", "element", "ip", "s2l_ppp_sessions", "peers", fmt.Sprintf("{ %s . %d }", p.PeerIP, p.Index))
	}
	stopGuard()
	<-guardDone
	if err := svc.Stop(m.ID); err != nil {
		t.Fatal(err)
	}
	peers, err := network.ReadPeers(st.ListMappings())
	if err != nil || len(peers) != 2 {
		t.Fatal("PPP links disappeared before revocation check", peers, err)
	}
	// The admission set uses the peer address and kernel interface index.
	for _, p := range peers {
		if p.MappingID == m.ID {
			if out, err := exec.Command("nft", "get", "element", "ip", "s2l_ppp_sessions", "peers", fmt.Sprintf("{ %s . %d }", p.PeerIP, p.Index)).CombinedOutput(); err == nil {
				t.Fatalf("stopped peer retained admission: %s", out)
			}
		}
	}
	offline(0)
	healthy(1)
	noFallback()
	t.Log("WAN L2TP: TCP/UDP/DNS passed on both selected interfaces; DNS DoH fallback and TCP recovery passed; WAN/TUN loss and synchronous stop did not use the default exit")
}

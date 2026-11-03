//go:build linux

package service

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// All commands run within the enclosing fixture's disposable namespaces. A
// reachable direct HTTPS endpoint makes zero-egress assertions meaningful even
// when the SOCKS endpoint is unavailable or the mapping TUN has disappeared.
func integrationDirectUplink(t *testing.T, root string, cert tls.Certificate, caPath string, start func(string, ...string)) func() {
	t.Helper()
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("direct-uplink fixture %v: %v %s", args, err, out)
		}
		return string(out)
	}
	const ns = "s2l-direct-uplink"
	run("ip", "netns", "add", ns)
	t.Cleanup(func() { exec.Command("ip", "netns", "del", ns).Run() })
	run("ip", "link", "add", "fixture-wan", "type", "veth", "peer", "name", "eth0", "netns", ns)
	run("ip", "addr", "add", "198.18.99.1/30", "dev", "fixture-wan")
	run("ip", "link", "set", "fixture-wan", "up")
	run("ip", "netns", "exec", ns, "ip", "addr", "add", "198.18.99.2/30", "dev", "eth0")
	run("ip", "netns", "exec", ns, "ip", "link", "set", "eth0", "up")
	run("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up")
	run("ip", "netns", "exec", ns, "ip", "addr", "add", "198.18.0.9/32", "dev", "lo")
	run("ip", "netns", "exec", ns, "ip", "route", "add", "default", "via", "198.18.99.1")
	run("ip", "route", "add", "default", "via", "198.18.99.2", "dev", "fixture-wan")
	keyPath := filepath.Join(root, "direct-uplink-key.pem")
	key := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(cert.PrivateKey.(*rsa.PrivateKey))})
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		t.Fatal(err)
	}
	// HTTPServer.server_bind performs a reverse DNS lookup before listening.
	// The isolated uplink has no resolver; use its literal address as the name
	// so this positive control does not depend on external DNS or retry timing.
	start("ip", "netns", "exec", ns, "python3", "-c", `import http.server,socketserver,ssl,sys
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  body=b'direct-uplink';self.send_response(200);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
 def log_message(self,*args):pass
class Server(http.server.HTTPServer):
 def server_bind(self):
  socketserver.TCPServer.server_bind(self)
  self.server_name,self.server_port=self.server_address[:2]
server=Server(('198.18.0.9',443),Handler)
tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);tls.load_cert_chain(sys.argv[1],sys.argv[2]);server.socket=tls.wrap_socket(server.socket,server_side=True)
server.serve_forever()
`, caPath, keyPath)
	var controlOutput []byte
	var controlErr error
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("direct-uplink positive control: %v %s", controlErr, controlOutput)
		}
	})
	integrationWait(t, 8*time.Second, func() bool {
		controlOutput, controlErr = exec.Command("curl", "--noproxy", "*", "--silent", "--show-error", "--fail", "--max-time", "1", "--cacert", caPath, "https://198.18.0.9/").CombinedOutput()
		return controlErr == nil && string(controlOutput) == "direct-uplink"
	})
	// Count all packets actually leaving for the direct uplink, including host
	// DNS bootstrap attempts and forwarded client traffic. Install after the
	// positive control so every subsequent packet is unexpected.
	// Keep bounded headers only, never payloads or credentials, so a nonzero
	// counter identifies the unexpected protocol/source instead of just a total.
	start("python3", "-u", "-c", `import json,socket,struct
s=socket.socket(socket.AF_PACKET,socket.SOCK_RAW,socket.htons(3));s.bind(('fixture-wan',0))
count=0
while True:
 p,a=s.recvfrom(65535)
 if a[2]!=socket.PACKET_OUTGOING or len(p)<34 or p[12:14]!=b'\x08\x00':continue
 h=14+(p[14]&15)*4;proto=p[23]
 row=dict(src=socket.inet_ntoa(p[26:30]),dst=socket.inet_ntoa(p[30:34]),proto=proto,length=int.from_bytes(p[16:18],'big'))
 if proto in (6,17) and len(p)>=h+4:row['ports']=struct.unpack('!HH',p[h:h+4])
 if proto==6 and len(p)>=h+14:row['tcp_flags']=p[h+13]
 if proto==1 and len(p)>=h+2:row['icmp_type_code']=list(p[h:h+2])
 if count<64:print('direct-uplink packet header: '+json.dumps(row),flush=True)
 count+=1
`)
	run("iptables", "-t", "mangle", "-N", "S2L_FIXTURE_EGRESS")
	run("iptables", "-t", "mangle", "-A", "S2L_FIXTURE_EGRESS", "-j", "RETURN")
	run("iptables", "-t", "mangle", "-A", "POSTROUTING", "-o", "fixture-wan", "-j", "S2L_FIXTURE_EGRESS")
	t.Log("direct-uplink positive control: trusted HTTPS target reachable without SOCKS; subsequent egress must stay at zero")
	return func() {
		t.Helper()
		out := run("iptables", "-t", "mangle", "-nvxL", "S2L_FIXTURE_EGRESS")
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[2] == "RETURN" {
				packets, err := strconv.ParseUint(fields[0], 10, 64)
				if err != nil || packets != 0 {
					t.Fatalf("traffic leaked to direct uplink: %s", out)
				}
				return
			}
		}
		t.Fatalf("missing direct-egress counter: %s", out)
	}
}

func integrationClientDNS(t *testing.T, ns, name, address string, healthy bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 16*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ip", "netns", "exec", ns, "python3", "-c", `import socket,struct,sys
def receive(s,n):
 data=b''
 while len(data)<n:
  part=s.recv(n-len(data))
  if not part:raise ConnectionError('DNS connection closed')
  data+=part
 return data
for wire in ['udp','tcp']:
 name=wire+'.'+sys.argv[1];healthy=sys.argv[3]=='true'
 q=struct.pack('!HHHHHH',1729,0x100,1,0,0,0)+b''.join(bytes([len(x)])+x.encode() for x in name.split('.'))+b'\x00\x00\x01\x00\x01'
 s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM if wire=='udp' else socket.SOCK_STREAM);s.settimeout(6)
 try:
  s.connect((sys.argv[2],53))
  if wire=='udp':s.send(q);r=s.recv(4096)
  else:s.sendall(struct.pack('!H',len(q))+q);r=receive(s,struct.unpack('!H',receive(s,2))[0])
  assert len(r)>=12 and r[:2]==q[:2],r
  if healthy:assert r[3]&15==0 and r[-4:]==bytes([198,18,0,9]),r
  else:assert r[3]&15 in (2,5),'DNS unexpectedly answered during outage: '+repr(r)
 except OSError:
  if healthy:raise
 finally:s.close()
`, name, address, strconv.FormatBool(healthy))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("client DNS %s %s healthy=%v: %v %s", ns, name, healthy, err, out)
	}
	t.Logf("client DNS %s: UDP/TCP to %s, healthy=%v, query=%s", ns, address, healthy, name)
}

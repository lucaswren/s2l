//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// IPsec peers get private runtime mounts as well as network namespaces. The
// server uses the real production configuration and firewall renderer.
func integrationIPsecAccess(t *testing.T, root, mode string, start func(string, ...string), run func(...string) string) func(int, string) string {
	t.Helper()
	encrypted := strings.HasPrefix(mode, "ipsec")
	nat := strings.Contains(mode, "nat")
	shared := strings.HasPrefix(mode, "ipsec-shared-nat")
	const gateway = "s2l-nat-gateway"
	const vpn = "198.18.99.1"
	addNS := func(ns string) {
		run("ip", "netns", "add", ns)
		t.Cleanup(func() { exec.Command("ip", "netns", "del", ns).Run() })
		run("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up")
	}
	peer := func(name, ns, conf string) string {
		dir := filepath.Join(root, "ipsec-"+name)
		if err := os.MkdirAll(filepath.Join(dir, "run"), 0700); err != nil {
			t.Fatal(err)
		}
		if name == "server" {
			// Share only the LNS control pipe and reply directory across the
			// fixture's private runtime mounts, as on the production host.
			runtime := filepath.Join(dir, "run", "s2l-ipsec-nat")
			if err := os.MkdirAll(runtime, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(root, filepath.Join(dir, "run", "xl2tpd")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(runtime, "/run/s2l-ipsec-nat"); err != nil {
				t.Fatal(err)
			}
		}
		files := map[string]string{
			"ipsec.conf": conf,
			"secrets":    ": PSK \"fixture-ipsec-secret\"\n",
			"strongswan.conf": fmt.Sprintf(`charon {
 load = random nonce openssl aes sha1 sha2 hmac kdf gmp kernel-netlink socket-default stroke updown
 user = root
 group = root
 install_routes = no
 plugins {
  stroke {
   secrets_file = %s/secrets
  }
 }
}
`, dir),
			"launch.py": `import os,pathlib,subprocess,sys
p=pathlib.Path(sys.argv[1])
subprocess.run(['mount','--bind',str(p/'run'),'/run'],check=True)
(p/'pid').write_text(str(os.getpid()))
os.environ['STRONGSWAN_CONF']=str(p/'strongswan.conf')
starter=next((x for x in ['/usr/lib/ipsec/starter','/usr/lib/strongswan/starter'] if pathlib.Path(x).is_file()),None)
if not starter:raise SystemExit('strongSwan starter is required')
os.execl(starter,starter,'--nofork','--conf',str(p/'ipsec.conf'))
`,
		}
		for name, data := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
		args := []string{"unshare", "--mount", "--propagation", "private", "python3", filepath.Join(dir, "launch.py"), dir}
		if ns != "" {
			args = append([]string{"ip", "netns", "exec", ns}, args...)
		}
		start(args[0], args[1:]...)
		var pid string
		integrationWait(t, 10*time.Second, func() bool {
			b, _ := os.ReadFile(filepath.Join(dir, "pid"))
			pid = strings.TrimSpace(string(b))
			if pid == "" {
				return false
			}
			out, err := exec.Command("nsenter", "-t", pid, "-m", "-n", "ipsec", "statusall").CombinedOutput()
			return err == nil && strings.Contains(string(out), "Connections:")
		})
		return pid
	}
	var serverPID string
	if encrypted {
		scripts := os.Getenv("S2L_FIXTURE_SCRIPT_DIR")
		if scripts == "" {
			t.Fatal("missing production script directory")
		}
		conf := run("python3", "-c", "import sys;sys.path.insert(0,sys.argv[1]);import manage_ipsec;sys.stdout.buffer.write(manage_ipsec.render_conf())", scripts)
		if mode == "ipsec-shared-nat-1701" {
			// Expose the real race: the initiator can send L2TP before the
			// responder's updown finishes. Delay, then execute the actual helper.
			wrapper := filepath.Join(root, "delayed-updown.py")
			os.WriteFile(wrapper, []byte("import os,sys,time\nif os.environ.get('PLUTO_VERB')=='up-host': time.sleep(2)\nos.execv(sys.executable,[sys.executable,sys.argv[1]])\n"), 0600)
			conf = strings.Replace(conf, "/usr/bin/python3 ", "/usr/bin/python3 "+wrapper+" ", 1)
		}
		run("python3", "-c", "import sys;sys.path.insert(0,sys.argv[1]);import manage_ipsec;manage_ipsec.firewall(True);assert manage_ipsec.firewall_protected();manage_ipsec.run('iptables','-D',manage_ipsec.CHAIN,'-p','udp','--dport','1701','-j','DROP');assert not manage_ipsec.firewall_protected();manage_ipsec.firewall(True);assert manage_ipsec.firewall_protected()", scripts)
		serverPID = peer("server", "", conf)
		run("iptables", "-t", "mangle", "-N", "S2L_FIXTURE_CLEAR")
		run("iptables", "-t", "mangle", "-A", "S2L_FIXTURE_CLEAR", "-j", "RETURN")
		run("iptables", "-t", "mangle", "-A", "POSTROUTING", "-p", "udp", "--sport", "1701", "-m", "policy", "--dir", "out", "--pol", "none", "-j", "S2L_FIXTURE_CLEAR")
		t.Logf("IPsec transport=%s, production server config and firewall loaded", mode)
	}
	if nat {
		addNS(gateway)
		run("ip", "link", "add", "dialgw", "type", "veth", "peer", "name", "wan", "netns", gateway)
		run("ip", "addr", "add", "192.0.2.1/29", "dev", "dialgw")
		run("ip", "link", "set", "dialgw", "up")
		run("ip", "route", "replace", "192.0.2.0/29", "dev", "dialgw", "src", vpn)
		run("ip", "netns", "exec", gateway, "ip", "addr", "add", "192.0.2.2/29", "dev", "wan")
		run("ip", "netns", "exec", gateway, "ip", "link", "set", "wan", "up")
		run("ip", "netns", "exec", gateway, "ip", "route", "add", "default", "via", "192.0.2.1")
		run("ip", "netns", "exec", gateway, "ip", "link", "add", "lan", "type", "bridge")
		run("ip", "netns", "exec", gateway, "ip", "addr", "add", "10.254.0.1/24", "dev", "lan")
		run("ip", "netns", "exec", gateway, "ip", "link", "set", "lan", "up")
		run("ip", "netns", "exec", gateway, "sysctl", "-qw", "net.ipv4.ip_forward=1")
		if shared {
			run("ip", "netns", "exec", gateway, "iptables", "-t", "nat", "-A", "POSTROUTING", "-o", "wan", "-j", "MASQUERADE")
		} else {
			run("ip", "netns", "exec", gateway, "ip", "addr", "add", "192.0.2.3/29", "dev", "wan")
			for i := 0; i < 2; i++ {
				run("ip", "netns", "exec", gateway, "iptables", "-t", "nat", "-A", "POSTROUTING", "-s", fmt.Sprintf("10.254.0.%d/32", i+2), "-o", "wan", "-j", "SNAT", "--to-source", fmt.Sprintf("192.0.2.%d", i+2))
			}
		}
	}
	return func(i int, ns string) string {
		t.Helper()
		addNS(ns)
		host := fmt.Sprintf("dial%d", i)
		server := fmt.Sprintf("192.0.2.%d", i*4+1)
		client := fmt.Sprintf("192.0.2.%d/30", i*4+2)
		via := server
		if nat {
			via = "10.254.0.1"
			client = fmt.Sprintf("10.254.0.%d/24", i+2)
			run("ip", "netns", "exec", gateway, "ip", "link", "add", host, "type", "veth", "peer", "name", "eth0", "netns", ns)
			run("ip", "netns", "exec", gateway, "ip", "link", "set", host, "master", "lan")
			run("ip", "netns", "exec", gateway, "ip", "link", "set", host, "up")
		} else {
			run("ip", "link", "add", host, "type", "veth", "peer", "name", "eth0", "netns", ns)
			run("ip", "addr", "add", server+"/30", "dev", host)
			run("ip", "link", "set", host, "up")
			if encrypted {
				// The synthetic access veth is distinct from the VPN address.
				// Keep wildcard xl2tpd sockets on the address negotiated by IKE.
				run("ip", "route", "replace", fmt.Sprintf("192.0.2.%d/30", i*4), "dev", host, "src", vpn)
			}
		}
		run("ip", "netns", "exec", ns, "ip", "addr", "add", client, "dev", "eth0")
		run("ip", "netns", "exec", ns, "ip", "link", "set", "eth0", "up")
		if !encrypted {
			return server
		}
		run("ip", "netns", "exec", ns, "ip", "route", "add", vpn+"/32", "via", via)
		run("ip", "netns", "exec", ns, "ip", "route", "add", "default", "via", via)
		// Bare L2TP is reachable at the IP layer, but must be blocked by the
		// production IPsec guard, before any client SA exists.
		run("ip", "netns", "exec", ns, "python3", "-c", `import socket
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.sendto(b'bare-l2tp-guard-probe',('198.18.99.1',1701))`)
		run("python3", "-c", `import subprocess,sys
rows=subprocess.check_output(['iptables','-nvxL','S2L-IPSEC'],text=True).splitlines()
assert sum(int(p[0]) for row in rows if len(p:=row.split())>2 and p[2]=='DROP')>=int(sys.argv[1]),'bare L2TP was not dropped'
`, fmt.Sprint(i+1))
		clientPort := 1701
		if mode == "ipsec-shared-nat" {
			clientPort += i
		}
		conf := fmt.Sprintf(`config setup
 uniqueids=no
conn fixture
 keyexchange=ikev1
 authby=secret
 type=transport
 left=%%defaultroute
 leftprotoport=17/%d
 right=%s
 rightprotoport=17/1701
 ike=aes256-sha256-modp2048!
 esp=aes256-sha256!
 rekey=no
 auto=add
`, clientPort, vpn)
		pid := peer(fmt.Sprintf("client%d", i), ns, conf)
		run("nsenter", "-t", pid, "-m", "-n", "ipsec", "up", "fixture")
		state := run("ip", "netns", "exec", ns, "ip", "xfrm", "state")
		if !strings.Contains(state, "mode transport") || !strings.Contains(state, "encap type espinudp") {
			t.Fatal("transport ESP with enforced NAT-T was not installed")
		}
		status := run("nsenter", "-t", serverPID, "-m", "-n", "ipsec", "statusall")
		if !strings.Contains(status, "ESTABLISHED") || !strings.Contains(status, "TRANSPORT") {
			t.Fatalf("IPsec SA not installed: %s", status)
		}
		// XFRM state contains encryption keys; only emit sanitized proof.
		t.Logf("client%d: IKEv1 authenticated, transport ESP installed, NAT-T=true, client behind NAT=%v", i, nat)
		return vpn
	}
}

func integrationAssertIPsecTraffic(t *testing.T, ns string) {
	t.Helper()
	out, err := exec.Command("ip", "netns", "exec", ns, "python3", "-c", `import re,subprocess
state=subprocess.check_output(['ip','-s','xfrm','state'],text=True)
counts=[int(x) for x in re.findall(r'(\d+)\(bytes\)',state)]
assert len(counts)==2 and all(x>0 for x in counts),'ESP has not carried bidirectional data'
print('ESP bytes per SA:',counts)
`).CombinedOutput()
	if err != nil {
		t.Fatalf("IPsec traffic proof: %v %s", err, out)
	}
	t.Logf("%s: %s", ns, strings.TrimSpace(string(out)))
	out, err = exec.Command("python3", "-c", `import subprocess
rows=subprocess.check_output(['iptables','-t','mangle','-nvxL','S2L_FIXTURE_CLEAR'],text=True).splitlines()
counts=[int(p[0]) for row in rows if len(p:=row.split())>2 and p[2]=='RETURN']
assert counts==[0],'server sent bare L2TP instead of ESP'
`).CombinedOutput()
	if err != nil {
		t.Fatalf("IPsec cleartext guard: %v %s", err, out)
	}
}

func integrationIPsecLifecycle(t *testing.T, root, caPath string, run func(...string) string) {
	t.Helper()
	pid := func(peer string) string {
		b, err := os.ReadFile(filepath.Join(root, "ipsec-"+peer, "pid"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	server, client := pid("server"), pid("client0")
	scripts := os.Getenv("S2L_FIXTURE_SCRIPT_DIR")
	t.Cleanup(func() {
		if t.Failed() {
			state, _ := os.ReadFile(filepath.Join(root, "ipsec-server", "run", "s2l-ipsec-nat", "state.json"))
			t.Logf("NAT journal (fixture addresses only): %s", state)
		}
	})
	request := func(i int) *exec.Cmd {
		return exec.Command("ip", "netns", "exec", fmt.Sprintf("s2l-dial-%d", i), "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "3", "--cacert", caPath, "https://198.18.0.9/")
	}
	healthy := func(i int) {
		t.Helper()
		var last string
		t.Cleanup(func() {
			if t.Failed() && last != "" {
				routes, _ := exec.Command("ip", "netns", "exec", fmt.Sprintf("s2l-dial-%d", i), "ip", "-4", "route").CombinedOutput()
				t.Logf("client%d last HTTP result: %s; routes: %s", i, last, routes)
			}
		})
		integrationWait(t, 15*time.Second, func() bool {
			out, err := request(i).CombinedOutput()
			ok := err == nil && strings.TrimSpace(string(out)) == fmt.Sprintf("node%d", i)
			last = ""
			if !ok {
				last = fmt.Sprintf("%v %s", err, out)
			}
			return ok
		})
	}
	// Repair ordering drift with both SAs active. Dynamic admission rules must
	// survive the atomic static-guard transaction without disconnecting clients.
	droppedBeforeRepair := strings.TrimSpace(run("python3", "-c", `import subprocess
rows=subprocess.check_output(['iptables','-nvxL','S2L-IPSEC'],text=True).splitlines()
print(sum(int(p[0]) for r in rows if len(p:=r.split())>2 and p[2]=='DROP'))
`))
	run("python3", "-c", `import sys
sys.path.insert(0,sys.argv[1]);import manage_ipsec as m
before=m.run('iptables','-S',m.PEER_CHAIN).stdout
m.run('iptables','-I','OUTPUT','-p','udp','--dport','9','-j','DROP')
assert not m.firewall_protected()
m.firewall(True)
assert m.firewall_protected() and m.run('iptables','-S',m.PEER_CHAIN).stdout==before
m.run('iptables','-D','OUTPUT','-p','udp','--dport','9','-j','DROP')
`, scripts)
	healthy(0)
	healthy(1)
	// Lose only the first SA's response mark. OUTPUT must drop its replies,
	// while the other client behind the identical NAT address continues working.
	const fault = `import json,sys
sys.path.insert(0,sys.argv[1]);import ipsec_nat as n
s=json.loads((n.ROOT/'state.json').read_text());a=min(s['active'].values(),key=lambda x:x['slot'])
table,chain,rule=n.rules(a)[1]
rule=['-m','comment','--comment','s2l-ipsec-nat',*rule]
n.run('iptables','-w','5','-t',table,sys.argv[2],chain,*rule)
`
	run("nsenter", "-t", server, "-m", "-n", "python3", "-c", fault, scripts, "-D")
	if out, err := request(0).CombinedOutput(); err == nil {
		t.Fatalf("missing IPsec response mark did not fail closed: %s", out)
	}
	healthy(1)
	run("python3", "-c", `import subprocess
rows=subprocess.check_output(['iptables','-nvxL','S2L-IPSEC-OUT'],text=True).splitlines()
assert any(int(p[0])>0 for r in rows if len(p:=r.split())>2 and p[2]=='DROP'),'output guard did not block bare replies'
`)
	run("nsenter", "-t", server, "-m", "-n", "python3", "-c", fault, scripts, "-I")
	healthy(0)
	integrationAssertIPsecTraffic(t, "s2l-dial-0")
	// A CHILD_SA rekey does not invoke updown again. Existing marks/zones must
	// continue carrying the same PPP session while the ESP SPI changes.
	const spi = `import re,subprocess
s=re.findall(r'\bspi (0x[0-9a-f]+)',subprocess.check_output(['ip','xfrm','state'],text=True))
print(','.join(sorted(s)) if len(s)>=2 else 'pending')
`
	oldSPI := run("ip", "netns", "exec", "s2l-dial-0", "python3", "-c", spi)
	run("nsenter", "-t", client, "-m", "-n", "ipsec", "stroke", "rekey", "fixture{}")
	integrationWait(t, 15*time.Second, func() bool {
		out, err := exec.Command("ip", "netns", "exec", "s2l-dial-0", "python3", "-c", spi).Output()
		return err == nil && string(out) != oldSPI && !strings.Contains(string(out), "pending")
	})
	// Explicit rekey with rekey=no can retain the old inbound SA until expiry.
	// Retire the old CHILD_SA now and prove data traverses the new SA alone.
	run("nsenter", "-t", client, "-m", "-n", "ipsec", "down", "fixture{1}")
	integrationWait(t, 15*time.Second, func() bool {
		out, err := exec.Command("ip", "netns", "exec", "s2l-dial-0", "python3", "-c", spi).Output()
		return err == nil && len(strings.Split(strings.TrimSpace(string(out)), ",")) == 2
	})
	healthy(0)
	healthy(1)
	t.Log("CHILD_SA SPI changed while both PPP clients retained their fixed exits")
	run("nsenter", "-t", client, "-m", "-n", "ipsec", "stroke", "rekey", "fixture")
	integrationWait(t, 15*time.Second, func() bool {
		out, err := exec.Command("nsenter", "-t", client, "-m", "-n", "ipsec", "statusall").Output()
		return err == nil && strings.Contains(string(out), "fixture[2]: ESTABLISHED")
	})
	healthy(0)
	healthy(1)
	t.Log("IKE_SA renewal preserved both fixed exits")
	control := filepath.Join(root, "client0.control")
	send := func(command string) {
		f, err := os.OpenFile(control, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(command + " vpn\n")
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	oldSlot := strings.TrimSpace(run("nsenter", "-t", server, "-m", "-n", "python3", "-c", `import json
from pathlib import Path
s=json.loads(Path('/run/s2l-ipsec-nat/state.json').read_text());assert len(s['active'])==2
print(min(x['slot'] for x in s['active'].values()))
`))
	send("d")
	integrationWait(t, 10*time.Second, func() bool {
		return exec.Command("ip", "netns", "exec", "s2l-dial-0", "ip", "link", "show", "ppp0").Run() != nil
	})
	// IKE reauthentication may leave a retiring IKE_SA alongside the current
	// one. Close all of this client's associations before initiating a redial.
	run("nsenter", "-t", client, "-m", "-n", "ipsec", "down", "fixture[*]")
	const retired = `import json,subprocess,sys
from pathlib import Path
s=json.loads(Path('/run/s2l-ipsec-nat/state.json').read_text());assert len(s['active'])==1
assert all(str(a['slot'])!=sys.argv[1] for a in s['active'].values())
r=subprocess.run(['conntrack','-L','-w',sys.argv[1]],capture_output=True,text=True)
assert r.returncode==0 and not r.stdout.strip(),'retired zone retained connections'
`
	// IKEv1 DELETE has no acknowledgement; the remote updown finishes later.
	var retiredError []byte
	t.Cleanup(func() {
		if t.Failed() && len(retiredError) > 0 {
			t.Logf("retired-zone check: %s", retiredError)
		}
	})
	integrationWait(t, 15*time.Second, func() bool {
		var err error
		retiredError, err = exec.Command("nsenter", "-t", server, "-m", "-n", "python3", "-c", retired, oldSlot).CombinedOutput()
		return err == nil
	})
	healthy(1)
	t.Log("retired conntrack zone empty and the other shared-NAT client remains healthy")
	run("nsenter", "-t", client, "-m", "-n", "ipsec", "up", "fixture")
	send("c")
	integrationWait(t, 30*time.Second, func() bool {
		// ppp0 exists before IPCP assigns IPv4. Like initial dialing, install
		// the fixture's default route only after address negotiation completes.
		out, err := exec.Command("ip", "netns", "exec", "s2l-dial-0", "ip", "-4", "-j", "address", "show", "dev", "ppp0").Output()
		if err != nil || !strings.Contains(string(out), `"local":"10.0.101.2"`) {
			return false
		}
		return exec.Command("ip", "netns", "exec", "s2l-dial-0", "ip", "route", "replace", "default", "dev", "ppp0").Run() == nil
	})
	healthy(0)
	healthy(1)
	integrationClientDNS(t, "s2l-dial-0", "reconnected.fixture.test", "1.1.1.1", true)
	integrationClientUDP(t, "s2l-dial-0", "node0", "allow", true)
	integrationAssertIPsecTraffic(t, "s2l-dial-0")
	run("python3", "-c", `import subprocess,sys
rows=subprocess.check_output(['iptables','-nvxL','S2L-IPSEC'],text=True).splitlines()
# Static-chain repair resets counters; retain both halves of the observation.
assert int(sys.argv[1])+sum(int(p[0]) for r in rows if len(p:=r.split())>2 and p[2]=='DROP')>2,'delayed updown did not exercise early encrypted arrivals'
`, droppedBeforeRepair)
	t.Log("shared NAT with identical 1701: response-mark failure blocked, other peer unaffected, CHILD_SA rekey and redial passed, retired conntrack zone removed")
	integrationIPsecRecycling(t, root, caPath, run)
}

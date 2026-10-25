//go:build linux

package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Exercise a deliberately exhausted high-water counter with real SAs, xl2tpd
// control replies, PPP and conntrack. The production quarantine is not shortened.
func integrationIPsecRecycling(t *testing.T, root, caPath string, run func(...string) string) {
	t.Helper()
	scripts := os.Getenv("S2L_FIXTURE_SCRIPT_DIR")
	pid := func(name string) string {
		data, err := os.ReadFile(filepath.Join(root, "ipsec-"+name, "pid"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(data))
	}
	server := pid("server")
	python := func(code string, args ...string) string {
		return run(append([]string{"nsenter", "-t", server, "-m", "-n", "python3", "-c",
			"import sys,json;sys.path.insert(0,sys.argv[1]);import ipsec_nat as n\n" + code, scripts}, args...)...)
	}
	poolSize := strings.TrimSpace(python(`s=json.loads((n.ROOT/'state.json').read_text())
assert len(s['active'])==2
peers=n.l2tp_peers()
assert len(peers)>=2,peers
for a in s['active'].values():
 assert (a['peer'],a['slot']) in peers,'live LNS endpoint absent from status'
 assert not n.unused_endpoint(a,peers,'',''),'live LNS was reclaimable'
# Simulate reaching the old cumulative limit; only proven retired slots can
# satisfy subsequent connections. Never modify kernel state to manufacture proof.
s['next']=n.LAST+1;n.save(s)
print(len(s['active'])+len(s['retired'])+len(s['free']))`))
	t.Logf("live LNS endpoints protected; forced high-water limit with %s reusable endpoints", poolSize)
	send := func(i int, command string) {
		f, err := os.OpenFile(filepath.Join(root, fmt.Sprintf("client%d.control", i)), os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(command + " vpn\n")
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	parallelIKE := func(clients []int, verb string) {
		jobs := make([]*exec.Cmd, 0, len(clients))
		for _, i := range clients {
			name := "fixture"
			if verb == "down" {
				name = "fixture[*]"
			}
			cmd := exec.Command("nsenter", "-t", pid(fmt.Sprintf("client%d", i)), "-m", "-n", "ipsec", verb, name)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			jobs = append(jobs, cmd)
		}
		for _, job := range jobs {
			if err := job.Wait(); err != nil {
				t.Fatalf("parallel IKE %s: %v", verb, err)
			}
		}
	}
	healthy := func(i int) {
		t.Helper()
		ns := fmt.Sprintf("s2l-dial-%d", i)
		integrationWait(t, 20*time.Second, func() bool {
			data, err := exec.Command("ip", "netns", "exec", ns, "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "3", "--cacert", caPath, "https://198.18.0.9/").Output()
			return err == nil && strings.TrimSpace(string(data)) == fmt.Sprintf("node%d", i)
		})
	}
	for round := 0; round < 4; round++ {
		clients := []int{0, 1}
		if round == 0 {
			clients = []int{0} // Reclaim one endpoint while the other stays online.
		}
		var stopMonitor context.CancelFunc
		var monitorDone chan struct{}
		var monitorFailure atomic.Value
		var monitorSamples atomic.Int32
		if round == 0 {
			ctx, cancel := context.WithCancel(context.Background())
			stopMonitor, monitorDone = cancel, make(chan struct{})
			t.Cleanup(cancel)
			go func() {
				defer close(monitorDone)
				for ctx.Err() == nil {
					data, err := exec.CommandContext(ctx, "ip", "netns", "exec", "s2l-dial-1", "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "3", "--cacert", caPath, "https://198.18.0.9/").Output()
					if ctx.Err() != nil {
						return
					}
					if err != nil || strings.TrimSpace(string(data)) != "node1" {
						monitorFailure.Store(fmt.Sprintf("%v %q", err, data))
					}
					monitorSamples.Add(1)
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Second):
					}
				}
			}()
		}
		for _, i := range clients {
			send(i, "d")
			integrationWait(t, 10*time.Second, func() bool {
				return exec.Command("ip", "netns", "exec", fmt.Sprintf("s2l-dial-%d", i), "ip", "link", "show", "ppp0").Run() != nil
			})
		}
		parallelIKE(clients, "down")
		integrationWait(t, 15*time.Second, func() bool {
			cmd := exec.Command("nsenter", "-t", server, "-m", "-n", "python3", "-c",
				`import json,sys;from pathlib import Path
s=json.loads(Path('/run/s2l-ipsec-nat/state.json').read_text());assert len(s['active'])==int(sys.argv[1])`, fmt.Sprint(2-len(clients)))
			return cmd.Run() == nil
		})
		if round == 0 {
			healthy(1)
		}
		// Retired endpoints must remain reserved during the actual 30s period.
		python(`s=json.loads((n.ROOT/'state.json').read_text());recent={k for k,a in s['retired'].items() if n.time.monotonic()-a['retired_at']<n.QUARANTINE}
assert recent
n.reclaim(s)
assert recent<=set(s['retired']),'quarantine bypassed'`)
		// Observe retirement without collecting it. The next real updown must
		// perform automatic collection itself, including the second observation
		// when the exhausted pool has never been inspected since retirement.
		deadline := time.Now().Add(90 * time.Second)
		for {
			status := python(`s=json.loads((n.ROOT/'state.json').read_text())
peers=n.l2tp_peers();sockets=n.run('ss','-H','-n','-u','-a','sport = :1701').stdout
xfrm=n.run('ip','xfrm','state').stdout+n.run('ip','xfrm','policy').stdout
print(sum(n.time.monotonic()-a['retired_at']>=n.QUARANTINE and n.unused_endpoint(a,peers,sockets,xfrm) for a in s['retired'].values())+len(s['free'])>=int(sys.argv[2]))`, fmt.Sprint(len(clients)))
			if strings.TrimSpace(status) == "True" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("retired endpoints did not become reclaimable")
			}
			time.Sleep(time.Second)
		}
		if round == 0 {
			t.Log(strings.TrimSpace(python(`import socket
s=json.loads((n.ROOT/'state.json').read_text());a=max(s['retired'].values(),key=lambda x:x['retired_at'])
sock=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)
sock.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
sock.bind((a['me'],1701));sock.connect((a['peer'],a['slot']))
def drops():
 rows=n.run('iptables','-nvxL','S2L-IPSEC-OUT').stdout.splitlines()
 return sum(int(p[0]) for r in rows if len(p:=r.split())>2 and p[2]=='DROP')
before=drops()
assert not n.unused_endpoint(a,n.l2tp_peers(),n.run('ss','-H','-n','-u','-a','sport = :1701').stdout,'')
assert a['blocked_by']=='UDP socket','retained socket was not identified independently of quarantine'
n.reclaim(s)
assert str(a['slot']) in s['retired'] and a['slot'] not in s['free'],'live UDP socket was reclaimed'
try:sock.send(b'retired-endpoint-must-not-leak')
except OSError:pass
assert drops()>before,'retired endpoint reply bypassed output guard'
sock.close()
# A broken control query is not an empty tunnel table.
original=n.CONTROL;n.CONTROL=n.ROOT/'missing-control'
n.reclaim(s)
assert str(a['slot']) in s['retired'],'query failure released a retired endpoint'
n.CONTROL=original
print('real retained UDP socket and unavailable control pipe both prevented reuse; stale reply was dropped')`)))
		}
		parallelIKE(clients, "up")
		for _, i := range clients {
			send(i, "c")
		}
		for _, i := range clients {
			ns := fmt.Sprintf("s2l-dial-%d", i)
			integrationWait(t, 30*time.Second, func() bool {
				data, err := exec.Command("ip", "netns", "exec", ns, "ip", "-4", "-j", "address", "show", "dev", "ppp0").Output()
				if err != nil || !strings.Contains(string(data), fmt.Sprintf(`"local":"10.0.%d.2"`, 101+i)) {
					return false
				}
				return exec.Command("ip", "netns", "exec", ns, "ip", "route", "replace", "default", "dev", "ppp0").Run() == nil
			})
			healthy(i)
			integrationClientDNS(t, ns, fmt.Sprintf("recycle-%d-%d.fixture.test", round, i), "1.1.1.1", true)
			policy := "allow"
			if i == 1 {
				policy = "reject-quic"
			}
			integrationClientUDP(t, ns, fmt.Sprintf("node%d", i), policy, true)
			integrationAssertIPsecTraffic(t, ns)
		}
		healthy(1)
		if stopMonitor != nil {
			stopMonitor()
			<-monitorDone
			if failure := monitorFailure.Load(); failure != nil {
				t.Fatalf("other client's fixed exit failed during recycling: %v", failure)
			}
			if monitorSamples.Load() < 3 {
				t.Fatal("insufficient concurrent fixed-exit observations")
			}
			t.Logf("other shared-NAT client remained online for %d fixed-exit probes during endpoint retirement and reuse", monitorSamples.Load())
		}
		t.Log(strings.TrimSpace(python(`s=json.loads((n.ROOT/'state.json').read_text());slots=[a['slot'] for a in s['active'].values()]
assert len(slots)==2 and len(set(slots))==2
assert s['next']==n.LAST+1 and max(slots)<n.FIRST+int(sys.argv[2])
assert len(s['free'])==len(set(s['free'])) and not set(s['free'])&set(slots)
assert len(s['active'])+len(s['retired'])+len(s['free'])==int(sys.argv[2])
print('reused active slots:',sorted(slots))`, poolSize)))
		t.Logf("recycle round %d: %d real redials, distinct reused endpoints, DNS/UDP/fixed SOCKS exits and ESP verified", round+1, len(clients))
	}
}

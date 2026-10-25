//go:build linux

package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/debuglog"
	"github.com/lucaswren/s2l/internal/l2tp"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/network"
	"github.com/lucaswren/s2l/internal/singbox"
	"github.com/lucaswren/s2l/internal/store"
)

// Run only through script/test_l2tp_integration.py in fresh mount AND network
// namespaces. Uses real xl2tpd, PPP, sing-box and two deterministic local SOCKS
// upstreams; no public network or installed host service is used.
func TestRealL2TPFixedExitsAndRecovery(t *testing.T) {
	if os.Getenv("S2L_L2TP_INTEGRATION") != "1" {
		t.Skip("requires isolated Linux PPP fixture")
	}
	debuglog.Default.Set(true)
	t.Cleanup(func() {
		if t.Failed() {
			data := debuglog.Default.Snapshot()
			if len(data) > 16000 {
				data = data[len(data)-16000:]
			}
			t.Log(string(data))
		}
		debuglog.Default.Set(false)
	})
	for _, kind := range []string{"mnt", "net"} {
		hostFD := os.Getenv("S2L_FIXTURE_HOST_" + strings.ToUpper(kind) + "_FD")
		if hostFD == "" {
			t.Fatal("missing host namespace references")
		}
		a, ae := os.Stat("/proc/self/ns/" + kind)
		b, be := os.Stat("/proc/self/fd/" + hostFD)
		if ae != nil || be != nil || os.SameFile(a, b) {
			t.Fatal("refusing host namespace", kind)
		}
	}
	if os.Geteuid() != 0 {
		t.Fatal("requires root in isolated namespace")
	}
	for _, bin := range []string{"ip", "iptables", "xl2tpd", "pppd", "curl", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatal(err)
		}
	}
	sbBin := os.Getenv("S2L_SINGBOX_CHECK_BIN")
	if sbBin == "" {
		t.Fatal("set S2L_SINGBOX_CHECK_BIN")
	}
	transport := os.Getenv("S2L_FIXTURE_TRANSPORT")
	ipsec := strings.HasPrefix(transport, "ipsec")
	root := t.TempDir()
	t.Cleanup(func() {
		if t.Failed() {
			files, _ := filepath.Glob(filepath.Join(root, "ppp-*.log"))
			for _, file := range files {
				data, _ := os.ReadFile(file)
				t.Logf("%s:\n%s", filepath.Base(file), data)
			}
		}
	})
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		b, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v %s", args[0], err, b)
		}
		return string(b)
	}
	run("ip", "link", "set", "lo", "up")
	tools := filepath.Join(root, "tools")
	os.MkdirAll(tools, 0700)
	// The fixture owns xl2tpd directly; never contact the host service manager.
	os.WriteFile(filepath.Join(tools, "systemctl"), []byte("#!/bin/sh\nif [ \"$1\" = is-active ]; then echo inactive; exit 3; fi\nexit 0\n"), 0700)
	t.Setenv("PATH", tools+":"+os.Getenv("PATH"))
	cert, ca := integrationCertificate(t)
	caPath := filepath.Join(root, "ca.pem")
	os.WriteFile(caPath, ca, 0600)
	t.Setenv("SSL_CERT_FILE", caPath)
	t.Setenv("CURL_CA_BUNDLE", caPath)
	st, err := store.New(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	sb := singbox.NewManager(sbBin)
	controller := network.NewController()
	l2 := l2tp.NewManager("/etc/ppp/chap-secrets", filepath.Join(root, "server.conf"), filepath.Join(root, "server.options"))
	svc := NewMappingService(st, sb, controller, l2)
	defer svc.Shutdown()
	eventCtx, eventCancel := context.WithCancel(context.Background())
	eventDone := make(chan struct{})
	go func() { defer close(eventDone); svc.listenPPP(eventCtx) }()
	defer func() { eventCancel(); <-eventDone }()
	integrationWait(t, 3*time.Second, func() bool { _, e := os.Stat(pppSocket); return e == nil })
	faults := map[string]func(bool){}
	proxyState := map[string]*integrationProxyState{}
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("node%d", i)
		addr, setOnline, state := integrationSOCKS(t, id, cert)
		faults[id] = setOnline
		proxyState[id] = state
		n := model.SocksNode{ID: id, Addr: addr}
		if err := st.UpsertNode(n); err != nil {
			t.Fatal(err)
		}
		m := model.TunnelMapping{ID: fmt.Sprintf("m%d", i), NodeID: id, TunName: fmt.Sprintf("tun%d", 101+i), TableID: 101 + i, Subnet: fmt.Sprintf("10.0.%d.0/24", 101+i), L2TPUser: fmt.Sprintf("user%d", i), L2TPPass: "fixture-secret", Status: model.StatusStopped, DNSMode: "tcp", UDPPolicy: "reject", UploadKbps: 20000, DownloadKbps: 20000, MaxConnections: 2, MaxSessions: 1, ObserveConnections: true}
		if ipsec {
			m.UDPPolicy = "allow"
			if i == 1 {
				m.UDPPolicy = "reject-quic"
			}
		}
		if i == 1 {
			m.DNSMode = "auto"
			m.DNSDoHURL = "https://resolver.fixture.invalid:8443/custom-dns"
			m.DNSTCPAddress = "dns.fixture.invalid:5353"
		}
		if err := st.UpsertMapping(m); err != nil {
			t.Fatal(err)
		}
		if err := svc.Start(m.ID); err != nil {
			t.Fatal(err)
		}
	}
	start := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		log, err := os.CreateTemp(root, "daemon-*.log")
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout = log
		cmd.Stderr = log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		t.Cleanup(func() {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				<-done
			}
			log.Close()
			if t.Failed() {
				b, _ := os.ReadFile(log.Name())
				t.Log(string(b))
			}
		})
	}
	assertNoDirectEgress := integrationDirectUplink(t, root, cert, caPath, start)
	access := integrationIPsecAccess(t, root, transport, start, run)
	if ipsec {
		if err := controller.EnsureGuard(); err != nil {
			t.Fatal(err)
		}
		run("python3", "-c", "import sys;sys.path.insert(0,sys.argv[1]);import manage_ipsec;assert manage_ipsec.firewall_protected()", os.Getenv("S2L_FIXTURE_SCRIPT_DIR"))
	}
	// Keep PPP negotiation evidence in this disposable fixture, not host syslog.
	serverOptions := filepath.Join(root, "server.options")
	optionsFile, err := os.OpenFile(serverOptions, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(optionsFile, "debug\nlogfile %s\n", filepath.Join(root, "ppp-server.log"))
	closeErr := optionsFile.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	start("xl2tpd", "-D", "-c", filepath.Join(root, "server.conf"), "-p", filepath.Join(root, "server.pid"), "-C", filepath.Join(root, "l2tp-control"))
	for i := 0; i < 2; i++ {
		ns := fmt.Sprintf("s2l-dial-%d", i)
		server := access(i, ns)
		options := filepath.Join(root, fmt.Sprintf("client%d.options", i))
		dnsHook, dnsFile := integrationClientDNSHook(t, root, i)
		conf := filepath.Join(root, fmt.Sprintf("client%d.conf", i))
		control := filepath.Join(root, fmt.Sprintf("client%d.control", i))
		// Colocated pppd 2.4.x peers share host entropy and can enter a
		// synchronized magic-number collision (EXIT_LOOPBACK). This fixture
		// tests real authentication, IPCP and forwarding, not serial-loop detection.
		// Disable magic negotiation on fixture clients only; keep production LCP.
		os.WriteFile(options, []byte(fmt.Sprintf("name user%d\npassword fixture-secret\nnoauth\nnomagic\nrefuse-eap\nnoccp\nnodefaultroute\nusepeerdns\nip-up-script %s\nmtu 1400\nmru 1400\ndebug\nlogfile %s\n", i, dnsHook, filepath.Join(root, fmt.Sprintf("ppp-client%d.log", i)))), 0600)
		clientConfig := fmt.Sprintf("[lac vpn]\nlns = %s\npppoptfile = %s\nlength bit = yes\n", server, options)
		if transport == "ipsec-shared-nat" {
			clientConfig = fmt.Sprintf("[global]\nport = %d\n", 1701+i) + clientConfig
		}
		os.WriteFile(conf, []byte(clientConfig), 0600)
		start("ip", "netns", "exec", ns, "xl2tpd", "-D", "-c", conf, "-p", filepath.Join(root, fmt.Sprintf("client%d.pid", i)), "-C", control)
		integrationWait(t, 5*time.Second, func() bool {
			f, e := os.OpenFile(control, os.O_WRONLY|syscall.O_NONBLOCK, 0)
			if e != nil {
				return false
			}
			defer f.Close()
			_, e = f.WriteString("c vpn\n")
			return e == nil
		})
		integrationWait(t, 45*time.Second, func() bool { return exec.Command("ip", "netns", "exec", ns, "ip", "link", "show", "ppp0").Run() == nil })
		integrationWait(t, 15*time.Second, func() bool {
			data, e := exec.Command("ip", "netns", "exec", ns, "ip", "-4", "-j", "address", "show", "dev", "ppp0").Output()
			if e != nil || !strings.Contains(string(data), fmt.Sprintf(`"local":"10.0.%d.2"`, 101+i)) {
				return false
			}
			peers, e := network.ReadPeers(st.ListMappings())
			ready := false
			for _, p := range peers {
				if p.MappingID == fmt.Sprintf("m%d", i) {
					ready = true
				}
			}
			if e != nil || !ready {
				return false
			}
			return exec.Command("ip", "netns", "exec", ns, "ip", "route", "replace", "default", "dev", "ppp0").Run() == nil
		})
		dnsAddresses := integrationAssertClientDNS(t, dnsFile)
		t.Logf("IPCP client%d received DNS %s (mapping resolver %s)", i, strings.Join(dnsAddresses, ", "), st.ListMappings()[i].EffectiveDNSMode())
		output := run("ip", "netns", "exec", ns, "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "5", "--cacert", caPath, "https://198.18.0.9/")
		if strings.TrimSpace(output) != fmt.Sprintf("node%d", i) {
			t.Fatal("wrong fixed exit", output)
		}
		// DNS is UDP on ingress, then TCP through the same SOCKS upstream. Other
		// UDP must receive an active rejection, rather than waiting for a timeout.
		run("ip", "netns", "exec", ns, "python3", "-c", `import socket,struct,time,sys
q=b'\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00\x05probe\x04test\x00\x00\x01\x00\x01'
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.settimeout(3);s.sendto(q,(sys.argv[1],53));r,_=s.recvfrom(4096);assert r[:2]==q[:2] and r[-4:]==bytes([198,18,0,9]);s.close()
q=b'\x12\x35\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00'+b''.join(bytes([len(x)])+x for x in [b'gmail',b'googleapis',b'com'])+b'\x00\x00\x41\x00\x01'
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.settimeout(3);s.sendto(q,(sys.argv[2],53));r,_=s.recvfrom(4096);assert r[:2]==q[:2] and r[3]&15==0 and r[6:8]==b'\x00\x01' and r.endswith(b'\x00\x01\x00\x00\x01\x00\x06\x02h2\x02h3'),'Google HTTPS record was altered or suppressed';s.close()
`, dnsAddresses[0], dnsAddresses[1])
		mapping, _ := st.GetMapping(fmt.Sprintf("m%d", i))
		integrationClientUDP(t, ns, fmt.Sprintf("node%d", i), mapping.UDPPolicy, true)
		// A client that retries over TCP after UDP rejection keeps this exit.
		output = run("ip", "netns", "exec", ns, "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "5", "--cacert", caPath, "https://198.18.0.9/")
		if strings.TrimSpace(output) != fmt.Sprintf("node%d", i) {
			t.Fatal("TCP retry changed fixed exit", output)
		}
	}
	// Check both again after the second IPsec SA is installed: a successful
	// initial dial does not prove that peers behind NAT can coexist.
	if ipsec {
		for i := 0; i < 2; i++ {
			ns := fmt.Sprintf("s2l-dial-%d", i)
			got := run("ip", "netns", "exec", ns, "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "5", "--cacert", caPath, "https://198.18.0.9/")
			if strings.TrimSpace(got) != fmt.Sprintf("node%d", i) {
				t.Fatal("IPsec peer displaced another fixed exit", got)
			}
			integrationAssertIPsecTraffic(t, ns)
		}
		if transport == "ipsec-shared-nat-1701" {
			integrationIPsecLifecycle(t, root, caPath, run)
		}
	}
	if proxyState["node1"].dohQueries.Load() == 0 || proxyState["node1"].tcpQueries.Load() != 0 {
		t.Fatal("custom DoH was not used exclusively while healthy")
	}
	// Use new names to bypass sing-box cache and prove both DNS ingress transports.
	proxyState["node1"].dohOnline.Store(false)
	for i := 0; i < 2; i++ {
		integrationClientDNS(t, "s2l-dial-1", fmt.Sprintf("fallback%d.fixture.test", i), "1.1.1.1", true)
	}
	if sb.ResolverStatus("tun102").Active != "tcp" || proxyState["node1"].tcpQueries.Load() < 2 {
		t.Fatal("custom DoH failure did not use the configured TCP resolver on the same node")
	}
	proxyState["node1"].assertDNSTargets(t, "resolver.fixture.invalid:8443", "dns.fixture.invalid:5353")
	assertNoDirectEgress()
	t.Log("custom DoH, configured TCP fallback and UDP/TCP client DNS passed through node1 only")
	if !ipsec {
		// Reusing an account must reject only the new excess session.
		dupNS := "s2l-dial-duplicate"
		run("ip", "netns", "add", dupNS)
		t.Cleanup(func() { exec.Command("ip", "netns", "del", dupNS).Run() })
		run("ip", "link", "add", "dialdup", "type", "veth", "peer", "name", "eth0", "netns", dupNS)
		run("ip", "addr", "add", "192.0.2.9/30", "dev", "dialdup")
		run("ip", "link", "set", "dialdup", "up")
		run("ip", "netns", "exec", dupNS, "ip", "addr", "add", "192.0.2.10/30", "dev", "eth0")
		run("ip", "netns", "exec", dupNS, "ip", "link", "set", "eth0", "up")
		run("ip", "netns", "exec", dupNS, "ip", "link", "set", "lo", "up")
		dupConf := filepath.Join(root, "duplicate.conf")
		dupControl := filepath.Join(root, "duplicate.control")
		os.WriteFile(dupConf, []byte(fmt.Sprintf("[lac vpn]\nlns = 192.0.2.9\npppoptfile = %s\nlength bit = yes\n", filepath.Join(root, "client0.options"))), 0600)
		start("ip", "netns", "exec", dupNS, "xl2tpd", "-D", "-c", dupConf, "-p", filepath.Join(root, "duplicate.pid"), "-C", dupControl)
		integrationWait(t, 5*time.Second, func() bool {
			f, e := os.OpenFile(dupControl, os.O_WRONLY|syscall.O_NONBLOCK, 0)
			if e != nil {
				return false
			}
			defer f.Close()
			_, e = f.WriteString("c vpn\n")
			return e == nil
		})
		integrationWait(t, 15*time.Second, func() bool {
			for _, ev := range svc.operations.View().Events {
				if ev.Kind == "session-limit" && strings.Contains(ev.Detail, "已拒绝") {
					return true
				}
			}
			return false
		})
		integrationWait(t, 5*time.Second, func() bool { rows, _ := network.ReadPeers(st.ListMappings()); return len(rows) == 2 })
	}
	first, _ := st.GetMapping("m0")
	second, _ := st.GetMapping("m1")
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		proof, err := svc.ProbeMapping(ctx, fmt.Sprintf("m%d", i))
		cancel()
		if err != nil || !proof.OK || proof.ExitComparison != "same" || proof.Forwarding.ExitIP != fmt.Sprintf("203.0.113.%d", 10+i) {
			t.Fatalf("full forwarding proof: %+v %v", proof, err)
		}
	}
	status := svc.Status()
	// The specialized report must use the live mapping listener. Its HTTPS RR
	// must survive the same fixed SOCKS upstream as real PPP client DNS above.
	dnsCtx, dnsCancel := context.WithTimeout(context.Background(), 20*time.Second)
	dnsReport, dnsErr := svc.ProbeGoogleMapping(dnsCtx, "m0", "node0")
	dnsCancel()
	if dnsErr != nil {
		t.Fatal(dnsErr)
	}
	for _, row := range dnsReport.Targets {
		if row.A.State != "healthy" || row.HTTPS.State != "healthy" || row.HTTPS.Records != 1 || row.DOH.State != "not_tested" || row.TCPDNS.State != "not_tested" {
			t.Fatalf("mapping DNS diagnostic bypassed runtime: %+v", row)
		}
	}
	if status.SessionsError != "" || len(status.Sessions) != 2 {
		t.Fatalf("PPP snapshot: %+v %s interfaces=%s", status.Sessions, status.SessionsError, run("ip", "-j", "address", "show"))
	}
	for _, peer := range status.Sessions {
		if peer.MappingID == "" || peer.MTU != 1400 || peer.RxBytes == 0 || peer.ConnectedAt.IsZero() || !peer.Disconnectable {
			t.Fatalf("PPP metadata missing: %+v", peer)
		}
	}
	// A held request must appear in this mapping only and support targeted close.
	held := exec.Command("ip", "netns", "exec", "s2l-dial-0", "curl", "--noproxy", "*", "--silent", "--max-time", "20", "--cacert", caPath, "https://198.18.0.9/hold")
	if e := held.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = held.Process.Kill(); _ = held.Wait() }()
	connectionID := ""
	integrationWait(t, 5*time.Second, func() bool {
		rows, e := svc.Connections(context.Background(), first.ID)
		if e != nil {
			return false
		}
		for _, r := range rows.Rows {
			if r.Metadata.Network == "tcp" && r.Metadata.DestinationIP == "198.18.0.9" && r.Metadata.DestinationPort == "443" {
				connectionID = r.ID
				return true
			}
		}
		return false
	})
	heldSecond := exec.Command("ip", "netns", "exec", "s2l-dial-0", "curl", "--noproxy", "*", "--silent", "--max-time", "20", "--cacert", caPath, "https://198.18.0.9/hold")
	if e := heldSecond.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = heldSecond.Process.Kill(); _ = heldSecond.Wait() }()
	integrationWait(t, 5*time.Second, func() bool {
		rows, e := svc.Connections(context.Background(), first.ID)
		if e != nil {
			return false
		}
		count := 0
		for _, r := range rows.Rows {
			if r.Metadata.Network == "tcp" && r.Metadata.DestinationIP == "198.18.0.9" && r.Metadata.DestinationPort == "443" {
				count++
			}
		}
		return count >= 2
	})
	limitedAt := time.Now()
	if e := exec.Command("ip", "netns", "exec", "s2l-dial-0", "curl", "--noproxy", "*", "--silent", "--max-time", "3", "--cacert", caPath, "https://198.18.0.9/").Run(); e == nil || time.Since(limitedAt) > 2*time.Second {
		t.Fatal("TCP connection limit did not actively reject new excess connection", e)
	}
	if got := run("ip", "netns", "exec", "s2l-dial-1", "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "5", "--cacert", caPath, "https://198.18.0.9/"); strings.TrimSpace(got) != "node1" {
		t.Fatal("connection limit affected another mapping", got)
	}
	otherConnections, e := svc.Connections(context.Background(), second.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range otherConnections.Rows {
		if r.ID == connectionID {
			t.Fatal("connection crossed mapping")
		}
	}
	if e = svc.CloseConnection(context.Background(), first.ID, connectionID); e != nil {
		t.Fatal(e)
	}
	// A real upstream outage must not restart a healthy local tunnel or move
	// either account onto the other node.
	faults[first.NodeID](false)
	assertClientOffline := func(index int, phase string) {
		t.Helper()
		ns := fmt.Sprintf("s2l-dial-%d", index)
		if out, e := exec.Command("ip", "netns", "exec", ns, "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "3", "--cacert", caPath, "https://198.18.0.9/").CombinedOutput(); e == nil {
			t.Fatalf("%s: offline client%d forwarded: %s", phase, index, out)
		}
		integrationClientDNS(t, ns, fmt.Sprintf("%s-client%d.fixture.test", phase, index), "8.8.8.8", false)
		m, _ := st.GetMapping(fmt.Sprintf("m%d", index))
		integrationClientUDP(t, ns, fmt.Sprintf("node%d", index), m.UDPPolicy, false)
	}
	assertClientOffline(0, "first-outage")
	if got := run("ip", "netns", "exec", "s2l-dial-1", "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "5", "--cacert", caPath, "https://198.18.0.9/"); strings.TrimSpace(got) != "node1" {
		t.Fatal("first outage affected the second fixed exit", got)
	}
	integrationClientDNS(t, "s2l-dial-1", "other-node-online.fixture.test", "1.1.1.1", true)
	integrationClientUDP(t, "s2l-dial-1", "node1", second.UDPPolicy, true)
	node, _ := st.GetNode(first.NodeID)
	pidBefore := sb.PID(first.TunName)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	report := svc.ProbeNode(ctx, node)
	cancel()
	if report.TCP.State == "healthy" {
		t.Fatal("upstream outage falsely healthy")
	}
	svc.checkLocalMapping(context.Background(), first)
	if sb.PID(first.TunName) != pidBefore {
		t.Fatal("upstream outage restarted local tunnel")
	}
	faults[second.NodeID](false)
	assertClientOffline(0, "both-outage")
	assertClientOffline(1, "both-outage")
	assertNoDirectEgress()
	peers, e := network.ReadPeers(st.ListMappings())
	if e != nil || len(peers) != 2 {
		t.Fatal("upstream outage disconnected PPP", peers, e)
	}
	faults[first.NodeID](true)
	faults[second.NodeID](true)
	for i := 0; i < 2; i++ {
		integrationClientDNS(t, fmt.Sprintf("s2l-dial-%d", i), fmt.Sprintf("recovered%d.fixture.test", i), "1.1.1.1", true)
		m, _ := st.GetMapping(fmt.Sprintf("m%d", i))
		integrationClientUDP(t, fmt.Sprintf("s2l-dial-%d", i), fmt.Sprintf("node%d", i), m.UDPPolicy, true)
	}
	t.Log("single and simultaneous upstream outages: client HTTPS/DNS fail, PPP stays connected, no direct egress; DNS recovers on the original nodes")
	// A real killed process must be detected and restored, without touching the
	// other mapping. No upstream selection or exit change is allowed.
	before := sb.PID(second.TunName)
	if err := syscall.Kill(-sb.PID(first.TunName), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	integrationWait(t, 5*time.Second, func() bool { return sb.PID(first.TunName) == 0 })
	if err := exec.Command("ip", "netns", "exec", "s2l-dial-0", "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "2", "--cacert", caPath, "https://198.18.0.9/").Run(); err == nil {
		t.Fatal("dead mapping leaked traffic")
	}
	integrationClientUDP(t, "s2l-dial-0", "node0", first.UDPPolicy, false)
	assertNoDirectEgress()
	svc.checkLocalMapping(context.Background(), first)
	if sb.PID(first.TunName) == 0 || sb.PID(second.TunName) != before {
		t.Fatal("recovery disrupted fixed mapping")
	}
	if err := controller.Verify(first); err != nil {
		t.Fatal(err)
	}
	// A limiter with the correct handle but the wrong rate is not healthy.
	run("tc", "qdisc", "replace", "dev", first.TunName, "root", "handle", "1:", "tbf", "rate", "10000kbit", "burst", "25000", "latency", "100ms")
	if controller.Verify(first) == nil {
		t.Fatal("upload rate drift was not detected")
	}
	run("tc", "qdisc", "replace", "dev", first.TunName, "root", "handle", "1:", "tbf", "rate", "20000kbit", "burst", "25000", "latency", "100ms")
	run("tc", "filter", "replace", "dev", first.TunName, "parent", "ffff:", "protocol", "ip", "pref", "10", "handle", "800::800", "u32", "match", "u32", "0", "0", "police", "rate", "10000kbit", "burst", "25000", "drop", "flowid", ":1")
	if controller.Verify(first) == nil {
		t.Fatal("download rate drift was not detected")
	}
	// Missing rules are repaired even when PPP collection is unavailable.
	svc.peerMu.Lock()
	svc.peersAt = time.Now()
	svc.peersError = "injected collection failure"
	svc.peerMu.Unlock()
	// Missing rules are repaired independently of upstream probes and without
	// replacing either mapping's live sing-box process.
	run("iptables", "-t", "nat", "-D", "POSTROUTING", "-o", first.TunName, "-j", "MASQUERADE", "-m", "comment", "--comment", "s2l:"+first.ID)
	if controller.Verify(first) == nil {
		t.Fatal("removed rule was not detected")
	}
	svc.mu.Lock()
	svc.health[first.ID].forwardingNext = time.Time{}
	svc.mu.Unlock()
	pid := sb.PID(first.TunName)
	svc.checkLocalMapping(context.Background(), first)
	if err := controller.Verify(first); err != nil {
		t.Fatal(err)
	}
	svc.peerMu.Lock()
	svc.peersAt = time.Time{}
	svc.peersError = ""
	svc.peerMu.Unlock()
	filters := run("tc", "filter", "show", "dev", first.TunName, "parent", "ffff:", "pref", "10")
	if strings.Count(filters, "police ") != 1 {
		t.Fatal("repair accumulated policers", filters)
	}
	if sb.PID(first.TunName) != pid || sb.PID(second.TunName) != before {
		t.Fatal("rule repair restarted a process")
	}
	// An already connected session can be terminated without restarting mappings.
	for i := 0; i < 2; i++ {
		got := run("ip", "netns", "exec", fmt.Sprintf("s2l-dial-%d", i), "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "5", "--cacert", caPath, "https://198.18.0.9/")
		if strings.TrimSpace(got) != fmt.Sprintf("node%d", i) {
			t.Fatal("exit changed after recovery", got)
		}
	}
	rows, _ := network.ReadPeers(st.ListMappings())
	for _, peer := range rows {
		if peer.MappingID == first.ID {
			if e := svc.DisconnectSession(context.Background(), peer.Interface, peer.PeerIP, peer.Index); e != nil {
				t.Fatal(e)
			}
			integrationWait(t, 5*time.Second, func() bool {
				peers, _ := network.ReadPeers(st.ListMappings())
				for _, p := range peers {
					if p.Index == peer.Index {
						return false
					}
				}
				return true
			})
		}
	}
	if sb.PID(second.TunName) != before {
		t.Fatal("session disconnect restarted other mapping")
	}
	assertNoDirectEgress()
	integrationWait(t, 3*time.Second, func() bool {
		for _, ev := range svc.operations.View().Events {
			if ev.Kind == "ppp-down" {
				return true
			}
		}
		return false
	})
	t.Log("verified PPP events, per-mapping bandwidth rules, active excess TCP rejection, connection isolation/close, upstream outage, process crash, NAT repair, fixed exits and targeted PPP disconnect")

}

func integrationWait(t *testing.T, budget time.Duration, check func() bool) {
	t.Helper()
	until := time.Now().Add(budget)
	for time.Now().Before(until) {
		if check() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("fixture readiness timed out")
}

func integrationCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "s2l-test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"resolver.fixture.invalid"}, IPAddresses: []net.IP{net.ParseIP("198.18.0.9"), net.ParseIP("1.1.1.1")}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert, certPEM
}

func integrationSOCKS(t *testing.T, id string, cert tls.Certificate, bind ...string) (string, func(bool), *integrationProxyState) {
	t.Helper()
	state := &integrationProxyState{online: true, connections: make(map[net.Conn]bool), dnsTargets: make(map[string]int)}
	state.dohOnline.Store(true)
	holdDone := make(chan struct{})
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/custom-dns" {
			state.dohQueries.Add(1)
			if !state.dohOnline.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			q, err := io.ReadAll(io.LimitReader(r.Body, 65536))
			if err != nil || len(q) < 12 || r.Method != "POST" || r.Header.Get("Content-Type") != "application/dns-message" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/dns-message")
			w.Write(integrationDNSAnswer(q))
			return
		}
		if r.URL.Path == "/hold" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-holdDone:
			}
			return
		}
		if r.URL.Path == "/cdn-cgi/trace" {
			fmt.Fprintf(w, "ip=203.0.113.%d\n", 10+int(id[len(id)-1]-'0'))
			return
		}
		fmt.Fprint(w, id)
	}))
	target.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	target.StartTLS()
	t.Cleanup(func() { close(holdDone); target.CloseClientConnections(); target.Close() })
	address := "127.0.0.1:0"
	if len(bind) > 0 {
		address = bind[0]
	}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				state.mu.Lock()
				if !state.online {
					state.mu.Unlock()
					return
				}
				state.connections[c] = true
				state.mu.Unlock()
				defer func() { state.mu.Lock(); delete(state.connections, c); state.mu.Unlock() }()
				c.SetDeadline(time.Now().Add(15 * time.Second))
				var greeting [2]byte
				if _, e := io.ReadFull(c, greeting[:]); e != nil {
					return
				}
				io.CopyN(io.Discard, c, int64(greeting[1]))
				c.Write([]byte{5, 0})
				var hdr [4]byte
				if _, e := io.ReadFull(c, hdr[:]); e != nil {
					return
				}
				var host string
				switch hdr[3] {
				case 1:
					address := make([]byte, 4)
					if _, e := io.ReadFull(c, address); e != nil {
						return
					}
					host = net.IP(address).String()
				case 4:
					address := make([]byte, 16)
					if _, e := io.ReadFull(c, address); e != nil {
						return
					}
					host = net.IP(address).String()
				case 3:
					var n [1]byte
					io.ReadFull(c, n[:])
					address := make([]byte, int(n[0]))
					if _, e := io.ReadFull(c, address); e != nil {
						return
					}
					host = string(address)
				default:
					return
				}
				var port [2]byte
				io.ReadFull(c, port[:])
				if hdr[1] == 3 {
					integrationUDPRelay(c, id)
					return
				}
				if hdr[1] != 1 {
					c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				dnsPort := uint16(53)
				if id == "node1" {
					dnsPort = 5353 // This node serves only its custom resolver port.
				}
				if p := binary.BigEndian.Uint16(port[:]); p == dnsPort || p == 8443 {
					state.mu.Lock()
					state.dnsTargets[net.JoinHostPort(host, fmt.Sprint(p))]++
					state.mu.Unlock()
				}
				if binary.BigEndian.Uint16(port[:]) == dnsPort {
					state.tcpQueries.Add(1)
					var size [2]byte
					if _, e := io.ReadFull(c, size[:]); e != nil {
						return
					}
					q := make([]byte, binary.BigEndian.Uint16(size[:]))
					if _, e := io.ReadFull(c, q); e != nil || len(q) < 12 {
						return
					}
					q = integrationDNSAnswer(q)
					binary.BigEndian.PutUint16(size[:], uint16(len(q)))
					c.Write(size[:])
					c.Write(q)
					return
				}
				up, e := net.DialTimeout("tcp", target.Listener.Addr().String(), time.Second)
				if e != nil {
					return
				}
				defer up.Close()
				go io.Copy(up, c)
				io.Copy(c, up)
			}()
		}
	}()
	return ln.Addr().String(), state.setOnline, state
}

type integrationProxyState struct {
	mu                     sync.Mutex
	online                 bool
	connections            map[net.Conn]bool
	dnsTargets             map[string]int
	dohOnline              atomic.Bool
	dohQueries, tcpQueries atomic.Int32
}

func (s *integrationProxyState) setOnline(online bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.online = online
	if !online {
		// A network outage also breaks pooled DNS and existing HTTPS connections.
		for conn := range s.connections {
			conn.Close()
		}
	}
}

func (s *integrationProxyState) assertDNSTargets(t *testing.T, expected ...string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.dnsTargets) != len(expected) {
		t.Fatalf("unexpected DNS destinations: %v", s.dnsTargets)
	}
	for _, target := range expected {
		if s.dnsTargets[target] == 0 {
			t.Fatalf("custom DNS target missing: %s; observed %v", target, s.dnsTargets)
		}
	}
	t.Logf("SOCKS CONNECT DNS destinations: %v", s.dnsTargets)
}

func integrationDNSAnswer(q []byte) []byte {
	q[2], q[3], q[6], q[7], q[8], q[9], q[10], q[11] = 0x81, 0x80, 0, 1, 0, 0, 0, 0
	if binary.BigEndian.Uint16(q[len(q)-4:]) == 65 {
		return append(q, []byte{0xc0, 0x0c, 0, 65, 0, 1, 0, 0, 0, 60, 0, 13, 0, 1, 0, 0, 1, 0, 6, 2, 'h', '2', 2, 'h', '3'}...)
	}
	return append(q, []byte{0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 198, 18, 0, 9}...)
}

// Capture IPCP negotiation in an isolated client hook rather than guessing DNS
// from server configuration or relying on the test host's resolver integration.
func integrationClientDNSHook(t *testing.T, root string, index int) (string, string) {
	t.Helper()
	hook := filepath.Join(root, fmt.Sprintf("client%d-dns-up", index))
	output := filepath.Join(root, fmt.Sprintf("client%d.dns", index))
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$DNS1\" \"$DNS2\" > %q\n", output)
	if err := os.WriteFile(hook, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return hook, output
}

func integrationAssertClientDNS(t *testing.T, path string) []string {
	t.Helper()
	var data []byte
	integrationWait(t, 10*time.Second, func() bool {
		data, _ = os.ReadFile(path)
		return string(data) == "1.1.1.1\n8.8.8.8\n"
	})
	return strings.Fields(string(data))
}

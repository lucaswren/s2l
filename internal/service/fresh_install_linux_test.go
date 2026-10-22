//go:build linux

package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This test may change host services only inside the disposable QEMU guest
// created by script/test_fresh_install.py. Ordinary CI and developer runs skip it.
func TestFreshInstalledServiceDial(t *testing.T) {
	if os.Getenv("S2L_FRESH_VM_TEST") != "1" {
		t.Skip("requires disposable full-system installation guest")
	}
	marker, err := os.ReadFile("/etc/s2l-fresh-vm")
	if err != nil || string(marker) != "disposable-qemu-s2l-fixture\n" || os.Geteuid() != 0 {
		t.Fatal("refusing non-fixture host")
	}
	root := t.TempDir()
	cert, ca := integrationCertificate(t)
	caPath := filepath.Join(root, "ca.pem")
	os.WriteFile(caPath, ca, 0600)
	addr, online := integrationSOCKS(t, "node0", cert, "127.0.0.1:31080")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 50 * time.Second}
	api := func(method, path string, body any) map[string]any {
		t.Helper()
		var input []byte
		if body != nil {
			input, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, "http://127.0.0.1:8080"+path, bytes.NewReader(input))
		req.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if resp.StatusCode != 200 {
			t.Fatalf("API %s: %d %s", path, resp.StatusCode, data)
		}
		result := map[string]any{}
		if e = json.Unmarshal(data, &result); e != nil {
			t.Fatal(e, string(data))
		}
		return result
	}
	api("POST", "/api/auth/login", map[string]any{"username": "fixture", "password": "fixture-test-password"})
	// Reboot repeats this test against the preserved running mapping and node.
	response, e := client.Get("http://127.0.0.1:8080/api/mappings")
	if e != nil {
		t.Fatal(e)
	}
	var mappings []map[string]any
	json.NewDecoder(response.Body).Decode(&mappings)
	response.Body.Close()
	if len(mappings) == 0 {
		api("POST", "/api/nodes", map[string]any{"id": "fixture-node", "name": "fixture-node", "addr": addr})
		for i := 0; i < 2; i++ {
			id := fmt.Sprintf("fixture%d", i)
			api("POST", "/api/mappings", map[string]any{"id": id, "node_id": "fixture-node", "tun_name": fmt.Sprintf("tun%d", 201+i), "table_id": 201 + i, "subnet": fmt.Sprintf("10.77.%d.0/24", 201+i), "l2tp_user": id, "l2tp_pass": "fixture-dial-password", "dns_mode": "tcp", "udp_policy": "reject", "max_sessions": 0})
			api("POST", "/api/mappings/"+id+"/action", map[string]any{"action": "start"})
		}
	} else {
		if len(mappings) != 2 {
			t.Fatal("mappings not restored after reboot", mappings)
		}
		for _, m := range mappings {
			if m["status"] != "running" || m["node_id"] != "fixture-node" {
				t.Fatal("shared-node mapping not restored after reboot", m)
			}
		}
	}
	run := func(name string, args ...string) string {
		t.Helper()
		out, e := exec.Command(name, args...).CombinedOutput()
		if e != nil {
			t.Fatalf("%s %v: %v %s", name, args, e, out)
		}
		return string(out)
	}
	startClient := func(index, account int) string {
		ns := fmt.Sprintf("fresh-dial-%d", index)
		host := fmt.Sprintf("fresh%d", index)
		run("ip", "netns", "add", ns)
		t.Cleanup(func() { exec.Command("ip", "netns", "del", ns).Run() })
		run("ip", "link", "add", host, "type", "veth", "peer", "name", "eth0", "netns", ns)
		server := fmt.Sprintf("192.0.2.%d", index*4+1)
		peer := fmt.Sprintf("192.0.2.%d/30", index*4+2)
		run("ip", "addr", "add", server+"/30", "dev", host)
		run("ip", "link", "set", host, "up")
		run("ip", "netns", "exec", ns, "ip", "addr", "add", peer, "dev", "eth0")
		run("ip", "netns", "exec", ns, "ip", "link", "set", "eth0", "up")
		run("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up")
		options := filepath.Join(root, fmt.Sprintf("client%d.options", index))
		conf := filepath.Join(root, fmt.Sprintf("client%d.conf", index))
		control := filepath.Join(root, fmt.Sprintf("client%d.control", index))
		os.WriteFile(options, []byte(fmt.Sprintf("name fixture%d\npassword fixture-dial-password\nnoauth\nnomagic\nrefuse-eap\nnoccp\nnodefaultroute\nmtu 1400\nmru 1400\n", account)), 0600)
		os.WriteFile(conf, []byte(fmt.Sprintf("[lac vpn]\nlns = %s\npppoptfile = %s\nlength bit = yes\n", server, options)), 0600)
		cmd := exec.Command("ip", "netns", "exec", ns, "xl2tpd", "-D", "-c", conf, "-p", conf+".pid", "-C", control)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		log, _ := os.Create(conf + ".log")
		cmd.Stdout = log
		cmd.Stderr = log
		if e := cmd.Start(); e != nil {
			t.Fatal(e)
		}
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		t.Cleanup(func() {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			log.Close()
			if t.Failed() {
				data, _ := os.ReadFile(conf + ".log")
				t.Log(string(data))
			}
		})
		integrationWait(t, 5*time.Second, func() bool {
			f, e := os.OpenFile(control, os.O_WRONLY|syscall.O_NONBLOCK, 0)
			if e != nil {
				return false
			}
			defer f.Close()
			_, e = f.WriteString("c vpn\n")
			return e == nil
		})
		return ns
	}
	clients := make([]string, 2)
	for i := range clients {
		ns := startClient(i, i)
		clients[i] = ns
		integrationWait(t, 45*time.Second, func() bool {
			out, e := exec.Command("ip", "netns", "exec", ns, "ip", "-j", "address", "show", "dev", "ppp0").Output()
			return e == nil && strings.Contains(string(out), fmt.Sprintf("10.77.%d.2", 201+i))
		})
		run("ip", "netns", "exec", ns, "ip", "route", "replace", "default", "dev", "ppp0")
		curlArgs := []string{"netns", "exec", ns, "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "4", "--cacert", caPath, "https://198.18.0.9/"}
		if got := run("ip", curlArgs...); strings.TrimSpace(got) != "node0" {
			t.Fatal("incorrect fixed exit", got)
		}
		run("ip", "netns", "exec", ns, "python3", "-c", `import socket,struct
q=struct.pack('!HHHHHH',71,0x100,1,0,0,0)+b'\x07example\x03com\x00'+struct.pack('!HH',1,1)
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.settimeout(5);s.sendto(q,('8.8.8.8',53));a=s.recv(4096)
assert a[:2]==q[:2] and a[-4:]==bytes([198,18,0,9]),a
`)
		run("ip", "netns", "exec", ns, "python3", "-c", `import socket
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.settimeout(3);s.connect(('198.18.0.9',443));s.send(b'quic-test')
try:s.recv(256);raise SystemExit('UDP was not rejected')
except ConnectionRefusedError:pass
`)
	}
	ns := clients[0]
	curlArgs := []string{"netns", "exec", ns, "curl", "--noproxy", "*", "--silent", "--fail", "--max-time", "4", "--cacert", caPath, "https://198.18.0.9/"}
	secondArgs := append([]string(nil), curlArgs...)
	secondArgs[2] = clients[1]
	assertSecond := func() {
		t.Helper()
		if got := run("ip", secondArgs...); strings.TrimSpace(got) != "node0" {
			t.Fatal("other gateway lost shared fixed exit", got)
		}
	}
	// Both PPP gateways must forward concurrently through the same exit, while
	// retaining separate accounts, peer addresses, TUNs and policy tables.
	for round := 0; round < 3; round++ {
		commands := make([]*exec.Cmd, 2)
		outputs := make([]bytes.Buffer, 2)
		for i, args := range [][]string{curlArgs, secondArgs} {
			traceArgs := append([]string(nil), args...)
			traceArgs[len(traceArgs)-1] = "https://198.18.0.9/cdn-cgi/trace"
			commands[i] = exec.Command("ip", traceArgs...)
			commands[i].Stdout = &outputs[i]
			commands[i].Stderr = &outputs[i]
			if err := commands[i].Start(); err != nil {
				t.Fatal(err)
			}
		}
		for i, cmd := range commands {
			if err := cmd.Wait(); err != nil || strings.TrimSpace(outputs[i].String()) != "ip=203.0.113.10" {
				t.Fatalf("concurrent gateway %d: %v %s", i, err, outputs[i].String())
			}
		}
	}
	// A legacy zero session limit cannot bypass fixed peer-address protection.
	before := api("GET", "/api/operations", nil)
	beforeEvents, _ := before["events"].([]any)
	startClient(2, 0)
	integrationWait(t, 20*time.Second, func() bool {
		after := api("GET", "/api/operations", nil)
		events, _ := after["events"].([]any)
		for _, ev := range events[len(beforeEvents):] {
			event, _ := ev.(map[string]any)
			if event["kind"] == "session-limit" && strings.Contains(fmt.Sprint(event["detail"]), "已拒绝") {
				return true
			}
		}
		return false
	})
	if got := run("ip", curlArgs...); strings.TrimSpace(got) != "node0" {
		t.Fatal("duplicate login displaced original session", got)
	}
	assertSecond()
	pid := func(tun string) int {
		status := api("GET", "/api/status", nil)
		pids, _ := status["singbox_pids"].(map[string]any)
		value, _ := pids[tun].(float64)
		return int(value)
	}
	secondPID := pid("tun202")
	oldPID := pid("tun201")
	if oldPID < 2 {
		t.Fatal("missing running process")
	}
	online(false)
	if exec.Command("ip", curlArgs...).Run() == nil {
		t.Fatal("offline upstream unexpectedly forwarded")
	}
	if exec.Command("ip", secondArgs...).Run() == nil {
		t.Fatal("second gateway leaked during shared upstream failure")
	}
	if pid("tun201") != oldPID || pid("tun202") != secondPID {
		t.Fatal("shared upstream failure restarted a mapping")
	}
	online(true)
	assertSecond()
	run("iptables", "-t", "nat", "-D", "POSTROUTING", "-o", "tun201", "-j", "MASQUERADE", "-m", "comment", "--comment", "s2l:fixture0")
	integrationWait(t, 45*time.Second, func() bool {
		return exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING", "-o", "tun201", "-j", "MASQUERADE", "-m", "comment", "--comment", "s2l:fixture0").Run() == nil
	})
	if pid("tun201") != oldPID {
		t.Fatal("rule repair restarted process")
	}
	assertSecond()
	if e := syscall.Kill(oldPID, syscall.SIGKILL); e != nil {
		t.Fatal(e)
	}
	integrationWait(t, 30*time.Second, func() bool { current := pid("tun201"); return current > 1 && current != oldPID })
	// A replacement PID precedes asynchronous TUN creation and route setup.
	// Require the actual client path to recover within a bounded interval.
	integrationWait(t, 20*time.Second, func() bool {
		out, err := exec.Command("ip", curlArgs...).CombinedOutput()
		return err == nil && strings.TrimSpace(string(out)) == "node0"
	})
	assertSecond()
	if pid("tun202") != secondPID {
		t.Fatal("first gateway recovery restarted other mapping")
	}
	api("POST", "/api/mappings/fixture0/action", map[string]any{"action": "stop"})
	if exec.Command("ip", curlArgs...).Run() == nil {
		t.Fatal("stopped mapping leaked or used the other mapping")
	}
	assertSecond()
	if pid("tun202") != secondPID {
		t.Fatal("stopping first gateway restarted second mapping")
	}
	// Leave both running so the next full-system boot tests automatic restore.
	api("POST", "/api/mappings/fixture0/action", map[string]any{"action": "start"})
	if got := run("ip", curlArgs...); strings.TrimSpace(got) != "node0" {
		t.Fatal("restarted gateway lost fixed exit", got)
	}
	t.Log("two independent gateways sharing one SOCKS node; installed systemd service: real PPP, duplicate rejection with legacy zero, UDP rejection, upstream outage, NAT repair, process recovery and fixed exit passed")
}

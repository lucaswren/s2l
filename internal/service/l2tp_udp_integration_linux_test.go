//go:build linux

package service

import (
	"encoding/binary"
	"io"
	"net"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// The relay belongs to its SOCKS TCP control connection, including during an
// injected upstream outage. Replies carry the selected node's distinct marker.
func integrationUDPRelay(control net.Conn, id string) {
	relay, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return
	}
	defer relay.Close()
	port := relay.LocalAddr().(*net.UDPAddr).Port
	reply := []byte{5, 0, 0, 1, 127, 0, 0, 1, byte(port >> 8), byte(port)}
	if _, err = control.Write(reply); err != nil {
		return
	}
	control.SetDeadline(time.Time{})
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, control); relay.Close(); close(done) }()
	defer func() { control.Close(); <-done }()
	buf := make([]byte, 65535)
	var client string
	for {
		n, from, err := relay.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n < 10 || buf[0] != 0 || buf[1] != 0 || buf[2] != 0 || buf[3] != 1 {
			continue
		}
		if client == "" {
			client = from.String()
		}
		if client != from.String() {
			continue
		}
		packet := append([]byte(nil), buf[:n]...)
		if binary.BigEndian.Uint16(packet[8:10]) == 53 {
			if n < 22 {
				continue
			}
			packet = append(packet[:10], integrationDNSAnswer(packet[10:])...)
		} else {
			if !net.IP(packet[4:8]).Equal(net.ParseIP("198.18.0.9")) {
				continue
			}
			packet = append(packet[:10], append([]byte(id+":"), packet[10:]...)...)
		}
		relay.WriteToUDP(packet, from)
	}
}

func integrationClientUDP(t *testing.T, ns, node, policy string, healthy bool) {
	t.Helper()
	out, err := exec.Command("ip", "netns", "exec", ns, "python3", "-c", `import errno,socket,sys
node,policy,healthy=sys.argv[1],sys.argv[2],sys.argv[3]=='true'
for port in (9999,443):
 reject=policy=='reject' or (policy=='reject-quic' and port==443)
 # Reuse the same five-tuple across calls, including outage and recovery.
 # A fresh random source port could hide a stale SOCKS UDP association.
 s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.bind(('0.0.0.0',40000+port%1000));s.settimeout(1.5);s.connect(('198.18.0.9',port))
 try:
  for size in (32,512,1200):
   payload=bytes((i%251 for i in range(size)))
   # Linux rate-limits ICMP errors per destination. Fast CI can exhaust the
   # burst while testing outages; waiting alone cannot resend the dropped
   # error. Retry the same datagram, still requiring a real port-unreachable.
   for attempt in range(4):
    try:
     s.send(payload);answer=s.recv(65535)
     assert healthy and not reject,'UDP bypassed policy or outage guard'
     assert answer==node.encode()+b':'+payload,'UDP fixed exit or payload changed'
     break
    except TimeoutError:
     if healthy and reject and attempt<3:
      print('retrying UDP rejection probe after timeout',port,attempt+1)
      continue
     assert not healthy,'UDP timed out without the required reply or active rejection'
     break
    except OSError as e:
     if healthy:assert reject and e.errno==errno.ECONNREFUSED,'expected ICMP port-unreachable: '+str(e)
     break
   if reject or not healthy:break
 finally:s.close()
`, node, policy, strconv.FormatBool(healthy)).CombinedOutput()
	if err != nil {
		t.Fatalf("UDP %s policy=%s healthy=%v: %v %s", ns, policy, healthy, err, out)
	}
	if len(out) > 0 {
		t.Logf("UDP %s: %s", ns, out)
	}
	t.Logf("UDP %s: policy=%s healthy=%v, ports 9999/443, payloads 32/512/1200 bytes", ns, policy, healthy)
}

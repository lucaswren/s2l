package network

import "testing"

func TestRejectCountersKeepMappingsAndProtocolsSeparate(t *testing.T) {
	text := `Chain S2L_FORWARD
 12 1000 REJECT udp -- ppp+ tun101 10.0.1.0/24 0.0.0.0/0 /* s2l-test-udp */ reject-with icmp-port-unreachable
 7 1000 REJECT tcp -- ppp+ tun101 10.0.1.0/24 0.0.0.0/0 /* s2l-test-connections */ reject-with tcp-reset
 99 1000 REJECT udp -- ppp+ tun102 10.0.2.0/24 0.0.0.0/0 /* s2l-test-other-udp */
 88 1000 ACCEPT udp -- ppp+ tun101 0/0 0/0 /* s2l-test-udp */
 0 0 REJECT udp -- ppp+ tun101 0/0 0/0 /* s2l-zero-udp */`
	r := parseRejectCounters(text, "s2l-test")
	if r.UDP != 12 || r.Connections != 7 || !r.UDPFound || !r.ConnectionsFound {
		t.Fatal(r)
	}
	r = parseRejectCounters(text, "s2l-zero")
	if r.UDP != 0 || !r.UDPFound || r.ConnectionsFound {
		t.Fatal(r)
	}
	r = parseRejectCounters(text, "missing")
	if r.UDPFound || r.ConnectionsFound {
		t.Fatal(r)
	}
}

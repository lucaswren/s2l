package model

import "testing"

func TestMappingOptionsCompatibilityAndBounds(t *testing.T) {
	m := TunnelMapping{}
	if err := m.ValidateOptions(); err != nil {
		t.Fatal(err)
	}
	if m.EffectiveMTU() != 1400 || m.EffectiveMSS() != 1200 || m.EffectiveQueueLength() != 1000 || m.EffectiveDNSMode() != "auto" || m.EffectiveUDPPolicy() != "reject-quic" {
		t.Fatal("legacy mapping defaults changed unexpectedly")
	}
	for _, m := range []TunnelMapping{{MTU: 1200}, {MTU: 1500}, {MSS: 1400}, {QueueLength: 1}, {UDPPolicy: "unknown"}, {DNSMode: "unknown"}} {
		if m.ValidateOptions() == nil {
			t.Fatalf("accepted invalid options: %+v", m)
		}
	}
}

package model

import "testing"

func TestSubnetRejectsDangerousNetworks(t *testing.T) {
	for _, s := range []string{"0.0.0.0/0", "127.0.0.0/8", "169.254.0.0/16", "224.0.0.0/4", "198.18.0.0/15", "8.8.8.0/24", "10.0.0.1/24", "10.0.0.0/31", "172.19.0.0/24", "172.16.0.0/12", "192.168.0.0/15", "::/0"} {
		if _, e := TunnelSubnet(s); e == nil {
			t.Fatal("unsafe subnet accepted", s)
		}
	}
	for _, s := range []string{"10.77.201.0/24", "192.168.201.0/30", "172.20.0.0/24"} {
		if _, e := TunnelSubnet(s); e != nil {
			t.Fatal(s, e)
		}
	}
	for _, n := range []int{0, 1, 2, 128} {
		if (TunnelMapping{MaxSessions: n}).EffectiveMaxSessions() != 1 {
			t.Fatal("duplicate peer allowed", n)
		}
	}
}

//go:build linux

package network

import (
	"testing"
)

func TestPolicerDetectsRateAndMatchDrift(t *testing.T) {
	text := "match 00000000/00000000 at 0\n police 0x1 rate 20Mbit burst 25000b action drop overhead 0b"
	if !validPolicer(text, 20000) || validPolicer(text, 10000) || validPolicer(text+"\n"+text, 20000) {
		t.Fatal("rate mismatch not detected")
	}
	if validPolicer("police 0x1 rate 20Mbit action drop", 20000) || validPolicer("match 00000000/00000000 at 0\n police 0x1 rate 20Mbit action pipe", 20000) {
		t.Fatal("changed filter accepted")
	}
}

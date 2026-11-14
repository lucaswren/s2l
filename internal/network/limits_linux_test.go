//go:build linux

package network

import "testing"

func TestDownloadRedirectMustTargetItsOwnQueue(t *testing.T) {
	redirect := "match 00000000/00000000 at 0\n action order 1: mirred (Egress Redirect to device s2lifb101) stolen"
	for _, row := range []struct {
		text string
		ok   bool
	}{
		{redirect, true},
		{redirect + "\n" + redirect, false},
		{"match 00000000/00000000 at 0\n action order 1: mirred (Egress Redirect to device s2lifb102) stolen", false},
		{"match 00000000/00000000 at 0\n police 0x1 rate 20Mbit action drop", false},
	} {
		if got := validDownloadRedirect(row.text, "s2lifb101"); got != row.ok {
			t.Fatalf("redirect acceptance = %t for %q", got, row.text)
		}
	}
}

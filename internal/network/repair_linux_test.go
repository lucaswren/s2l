//go:build linux

package network

import (
	"context"
	"errors"
	"github.com/lucaswren/s2l/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestMissingFirewallRequiresConfirmedAbsence(t *testing.T) {
	for _, tc := range []struct {
		script  string
		missing bool
	}{
		{"echo 'Bad rule (does a matching rule exist in that chain?).' >&2; exit 1", true},
		{"echo 'Permission denied' >&2; exit 1", false},
		{"echo 'Bad rule' >&2; exit 4", false},
		{"exit 1", false},
	} {
		err := runContext(context.Background(), "sh", "-c", tc.script)
		if IsMissingState(missingFirewall(err, "iptables", []string{"-C", "FORWARD"})) != tc.missing {
			t.Fatal(tc, err)
		}
	}
	if IsMissingState(missingFirewall(context.Canceled, "iptables", nil)) {
		t.Fatal("cancellation authorized mutation")
	}
}
func TestMalformedRouteSnapshotDoesNotAuthorizeRepair(t *testing.T) {
	m := model.TunnelMapping{TunName: "tun101", Subnet: "10.0.101.0/24", TableID: 101}
	for _, rules := range []string{"[]", "broken"} {
		err := verifyRouteJSON(m, []byte(rules), []byte("broken"))
		if err == nil || IsMissingState(err) {
			t.Fatal("invalid inspection authorized repair", err)
		}
	}
	err := verifyRouteJSON(m, []byte("[]"), []byte("[]"))
	var missing *MissingState
	if !errors.As(err, &missing) || missing.command != "ip" || missing.args[2] != "unreachable" {
		t.Fatal("fallback guard must be repaired first", err)
	}
}
func TestInterfaceInspectionIsReadOnly(t *testing.T) {
	if present, err := InterfacePresent("lo"); err != nil || !present {
		t.Fatal(present, err)
	}
	if present, err := InterfacePresent("s2l-absent-test"); err != nil || present {
		t.Fatal(present, err)
	}
}

func TestRepairRefusesUnknownInspectionWithoutMutations(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	m := model.TunnelMapping{TunName: "tun101", Subnet: "10.0.101.0/24", TableID: 101}
	ip := `#!/bin/sh
case "$*" in
 "-N -j rule show") echo '[{"src":"10.0.101.0/24","table":101,"priority":10101}]';;
 "-j route show table 101") echo '[{"dst":"default","dev":"tun101","metric":100},{"dst":"default","type":"unreachable","metric":32767}]';;
 *) echo mutation >> "$S2L_REPAIR_TEST_LOG"; exit 99;;
esac
`
	if e := os.WriteFile(filepath.Join(dir, "ip"), []byte(ip), 0700); e != nil {
		t.Fatal(e)
	}
	log := filepath.Join(dir, "mutations")
	t.Setenv("S2L_REPAIR_TEST_LOG", log)
	for _, failure := range []string{"Permission denied", "Another app is currently holding the xtables lock"} {
		script := "#!/bin/sh\ncase \"$*\" in *-C*) echo '" + failure + "' >&2; exit 1;; *) echo mutation >> \"$S2L_REPAIR_TEST_LOG\"; exit 99;; esac\n"
		// Build shell text directly, without interpolating paths or command arguments.
		if e := os.WriteFile(filepath.Join(dir, "iptables"), []byte(script), 0700); e != nil {
			t.Fatal(e)
		}
		if err := NewController().RepairContext(context.Background(), m); err == nil || IsMissingState(err) {
			t.Fatal("unknown failure authorized repair", err)
		}
		if _, err := os.Stat(log); !os.IsNotExist(err) {
			t.Fatal("inspection failure mutated network state", err)
		}
	}
}

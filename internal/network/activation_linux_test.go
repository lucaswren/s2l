//go:build linux

package network

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func TestFirewallNeverOpensIngressBeforeNATAndRestrictions(t *testing.T) {
	dir := t.TempDir()
	// Model the kernel rule inventory and reject any period with open ingress
	// but missing NAT/MSS/return/restrictions. Inject failures before activation.
	script := `#!/bin/sh
case "$*" in
 *" -C "*|*" -D "*)
  case "$*" in *" -D S2L_FORWARD "*" -j ACCEPT "*) rm -f "$S2L_ACTIVATION_STATE/open";; esac
  echo 'Bad rule (does a matching rule exist in that chain?).' >&2; exit 1;;
esac
case "$*" in
 *" -t mangle -I "*) touch "$S2L_ACTIVATION_STATE/mss";;
 *" -t nat -A "*) touch "$S2L_ACTIVATION_STATE/nat";;
 *" -I S2L_RETURN "*) touch "$S2L_ACTIVATION_STATE/return";;
 *" -I S2L_FORWARD "*" -j REJECT "*) touch "$S2L_ACTIVATION_STATE/restrict";;
 *" -I S2L_FORWARD "*" -j ACCEPT "*)
  for rule in nat mss return restrict; do
   test -e "$S2L_ACTIVATION_STATE/$rule" || { echo "premature ingress: $rule" >&2; exit 99; }
  done
  case "$*" in *" -I S2L_FORWARD 3 "*) ;; *) echo 'restriction bypass' >&2; exit 98;; esac
  touch "$S2L_ACTIVATION_STATE/open";;
esac
if [ -n "$S2L_ACTIVATION_FAIL" ]; then
 case "$*" in *"$S2L_ACTIVATION_FAIL"*) echo 'injected rule failure' >&2; exit 2;; esac
fi
`
	if err := os.WriteFile(filepath.Join(dir, "iptables"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	m := model.TunnelMapping{ID: "activation", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", UDPPolicy: "reject", MaxConnections: 10}
	for _, failure := range []string{"", "-t nat -A", "-I S2L_RETURN", "--connlimit-above", "-I S2L_FORWARD 3"} {
		t.Run(failure, func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("S2L_ACTIVATION_STATE", state)
			t.Setenv("S2L_ACTIVATION_FAIL", failure)
			err := AddFirewall(m)
			_, openErr := os.Stat(filepath.Join(state, "open"))
			if failure == "" {
				if err != nil || openErr != nil {
					t.Fatal("fully prepared path did not open", err, openErr)
				}
			} else if err == nil || !strings.Contains(err.Error(), "injected rule failure") || !os.IsNotExist(openErr) {
				t.Fatal("failed setup left ingress open or failed before injection", err, openErr)
			}
		})
	}
}

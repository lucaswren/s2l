//go:build linux

package network

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucaswren/s2l/internal/model"
)

func TestReadRejectCountersUsesOnlyReadOnlyCommand(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
test "$*" = '-w 5 -n -v -x -L S2L_FORWARD' || exit 2
printf '%s\n' '5 300 REJECT udp -- ppp+ tun1 10.0.1.0/24 0/0 /* s2l:m-udp */'
`
	path := filepath.Join(dir, "iptables")
	if e := os.WriteFile(path, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	mappings := []model.TunnelMapping{{ID: "m"}, {ID: "other"}}
	r := ReadRejectCounters(context.Background(), mappings)
	if r["m"].UDP != 5 || !r["m"].UDPFound || r["other"].UDPFound {
		t.Fatal(r)
	}
	if e := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0700); e != nil {
		t.Fatal(e)
	}
	r = ReadRejectCounters(context.Background(), mappings)
	if r["m"].State != "unavailable" || r["other"].State != "unavailable" {
		t.Fatal(r)
	}
}

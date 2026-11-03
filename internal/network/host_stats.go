package network

import (
	"os"
	"strconv"
	"strings"
)

// Host-wide evidence, not per-client packet loss or tunnel-specific retransmits.
func HostStats() map[string]uint64 {
	out := map[string]uint64{}
	b, e := os.ReadFile("/proc/net/snmp")
	if e == nil {
		lines := strings.Split(string(b), "\n")
		for i := 0; i+1 < len(lines); i++ {
			keys, values := strings.Fields(lines[i]), strings.Fields(lines[i+1])
			if len(keys) == len(values) && len(keys) > 0 && keys[0] == "Tcp:" && values[0] == "Tcp:" {
				for j, k := range keys {
					if k == "RetransSegs" || k == "OutSegs" || k == "CurrEstab" {
						if v, err := strconv.ParseUint(values[j], 10, 64); err == nil {
							out[k] = v
						}
					}
				}
			}
		}
	}
	for key, path := range map[string]string{"conntrack_count": "/proc/sys/net/netfilter/nf_conntrack_count", "conntrack_max": "/proc/sys/net/netfilter/nf_conntrack_max"} {
		if b, e := os.ReadFile(path); e == nil {
			if v, e := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); e == nil {
				out[key] = v
			}
		}
	}
	return out
}

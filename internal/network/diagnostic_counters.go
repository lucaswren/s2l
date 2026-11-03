package network

import (
	"strconv"
	"strings"
)

// RejectCounters are aggregate mapping evidence, never per-device attribution.
type RejectCounters struct {
	State            string `json:"state"`
	UDP              uint64 `json:"udp"`
	Connections      uint64 `json:"connections"`
	UDPFound         bool   `json:"udp_found"`
	ConnectionsFound bool   `json:"connections_found"`
}

func parseRejectCounters(text, comment string) RejectCounters {
	r := RejectCounters{State: "healthy"}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[2] != "REJECT" {
			continue
		}
		packets, e := strconv.ParseUint(f[0], 10, 64)
		if e != nil {
			continue
		}
		if f[3] == "udp" && strings.Contains(line, "/* "+comment+"-udp */") {
			r.UDP += packets
			r.UDPFound = true
		}
		if f[3] == "tcp" && strings.Contains(line, "/* "+comment+"-connections */") {
			r.Connections += packets
			r.ConnectionsFound = true
		}
	}
	return r
}

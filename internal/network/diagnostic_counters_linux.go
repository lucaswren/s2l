//go:build linux

package network

import (
	"context"
	"github.com/lucaswren/s2l/internal/model"
	"time"
)

// ReadRejectCounters never resets counters, repairs rules or sends probes.
func ReadRejectCounters(ctx context.Context, mappings []model.TunnelMapping) map[string]RejectCounters {
	result := map[string]RejectCounters{}
	if len(mappings) == 0 {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	text, err := outputContext(ctx, "iptables", "-n", "-v", "-x", "-L", "S2L_FORWARD")
	for _, m := range mappings {
		if err != nil {
			result[m.ID] = RejectCounters{State: "unavailable"}
		} else {
			result[m.ID] = parseRejectCounters(text, mappingComment(m))
		}
	}
	return result
}

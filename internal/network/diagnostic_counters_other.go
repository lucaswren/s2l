//go:build !linux

package network

import (
	"context"
	"github.com/lucaswren/s2l/internal/model"
)

func ReadRejectCounters(_ context.Context, mappings []model.TunnelMapping) map[string]RejectCounters {
	r := map[string]RejectCounters{}
	for _, m := range mappings {
		r[m.ID] = RejectCounters{State: "unsupported"}
	}
	return r
}

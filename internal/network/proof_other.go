//go:build !linux

package network

import (
	"context"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func ProbeForwarding(context.Context, model.TunnelMapping) model.ProtocolHealth {
	return model.ProtocolHealth{State: "unsupported", Method: "ppp-ingress-https", Error: "映射实测需要 Linux", CheckedAt: time.Now()}
}
func CleanupProofs() error { return nil }

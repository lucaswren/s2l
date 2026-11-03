//go:build !linux

package network

import (
	"context"
	"fmt"

	"github.com/lucaswren/s2l/internal/model"
)

func ReadPeers([]model.TunnelMapping) ([]model.PPPSession, error) {
	return nil, fmt.Errorf("PPP 会话需要 Linux")
}
func TunePeerSnapshot(model.TunnelMapping, []model.PPPSession) error { return nil }

func ReadPeersContext(context.Context, []model.TunnelMapping) ([]model.PPPSession, error) {
	return nil, fmt.Errorf("PPP 会话需要 Linux")
}

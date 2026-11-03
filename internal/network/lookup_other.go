//go:build !linux

package network

import (
	"context"
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
)

func VerifyPeerRoutes(context.Context, model.TunnelMapping, []model.PPPSession) error {
	return fmt.Errorf("forwarded route verification requires Linux")
}

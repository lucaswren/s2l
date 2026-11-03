//go:build !linux

package service

import (
	"context"
	"fmt"
)

func pppIdentity(int) (string, error)                   { return "", fmt.Errorf("requires Linux") }
func (s *MappingService) listenPPP(ctx context.Context) { <-ctx.Done() }
func (s *MappingService) DisconnectSession(context.Context, string, string, int) error {
	return fmt.Errorf("requires Linux")
}

func (s *MappingService) disconnectSession(context.Context, string, string, int, string) error {
	return fmt.Errorf("requires Linux")
}

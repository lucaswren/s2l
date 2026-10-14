package service

import (
	"context"
	"fmt"
	"github.com/lucaswren/s2l/internal/diagnostics"
	"github.com/lucaswren/s2l/internal/model"
)

func (s *MappingService) Diagnose(ctx context.Context, id, target string) (diagnostics.Result, error) {
	m, ok := s.store.GetMapping(id)
	if !ok || m.Status != model.StatusRunning {
		return diagnostics.Result{}, fmt.Errorf("start the mapping before running diagnostics")
	}
	node, ok := s.store.GetNode(m.NodeID)
	if !ok {
		return diagnostics.Result{}, fmt.Errorf("upstream node missing")
	}
	return diagnostics.Diagnose(ctx, node, target, func(ctx context.Context, q []byte) ([]byte, error) { return s.singbox.ResolveDNS(ctx, m.TunName, q) })
}

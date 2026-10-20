package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/lockutil"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/network"
	"github.com/lucaswren/s2l/internal/speedtest"
)

// ProbeMapping serializes only this mapping's lifecycle. It neither repairs nor
// restarts anything: a diagnostic must observe the failure it was asked to test.
func (s *MappingService) ProbeMapping(ctx context.Context, id string) (model.MappingDiagnostic, error) {
	if err := lockutil.RLock(ctx, &s.stateGate); err != nil {
		return model.MappingDiagnostic{}, err
	}
	defer s.stateGate.RUnlock()
	value, _ := s.operationLocks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	if err := lockutil.Lock(ctx, mu); err != nil {
		return model.MappingDiagnostic{}, err
	}
	defer mu.Unlock()
	m, ok := s.store.GetMapping(id)
	if !ok || m.Status != model.StatusRunning {
		return model.MappingDiagnostic{}, fmt.Errorf("映射未运行")
	}
	r := model.MappingDiagnostic{MappingID: id, CheckedAt: time.Now(), UDPPolicy: m.EffectiveUDPPolicy(), Scope: "PPP 模拟入口 → 现有策略路由与防火墙 → TUN → 固定 SOCKS5；不包含 L2TP 认证"}
	if err := s.net.VerifyContext(ctx, m); err != nil {
		r.Rules = model.ProtocolHealth{State: "unavailable", Method: "kernel-rules", Error: "本地转发规则检查失败", CheckedAt: time.Now()}
	} else {
		r.Rules = model.ProtocolHealth{State: "healthy", Method: "kernel-rules", CheckedAt: time.Now()}
	}
	node, nodeOK := s.store.GetNode(m.NodeID)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); r.Forwarding = network.ProbeForwarding(ctx, m) }()
	if nodeOK {
		r.Node = speedtest.ProbeExitIP(ctx, node)
	} else {
		r.Node = model.ProtocolHealth{State: "unavailable", Error: "节点不存在"}
	}
	wg.Wait()
	r.ExitComparison = "unknown"
	if r.Node.ExitIP != "" && r.Forwarding.ExitIP != "" {
		r.ExitComparison = "same"
		if r.Node.ExitIP != r.Forwarding.ExitIP {
			r.ExitComparison = "different"
		}
	}
	r.OK = r.Rules.State == "healthy" && r.Forwarding.State == "healthy" && r.Node.State == "healthy"
	if s.history != nil && ctx.Err() == nil {
		s.history.RecordDiagnostic(id, r)
	}
	return r, nil
}

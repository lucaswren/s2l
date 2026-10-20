package service

import (
	"context"
	"fmt"
	"github.com/lucaswren/s2l/internal/lockutil"
	"github.com/lucaswren/s2l/internal/singbox"
	"sync"
)

func (s *MappingService) Connections(ctx context.Context, id string) (singbox.Connections, error) {
	if err := lockutil.RLock(ctx, &s.stateGate); err != nil {
		return singbox.Connections{}, err
	}
	defer s.stateGate.RUnlock()
	value, _ := s.operationLocks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	if err := lockutil.Lock(ctx, mu); err != nil {
		return singbox.Connections{}, err
	}
	defer mu.Unlock()
	m, ok := s.store.GetMapping(id)
	if !ok {
		return singbox.Connections{}, fmt.Errorf("映射不存在")
	}
	return s.singbox.Connections(ctx, m.TunName)
}
func (s *MappingService) CloseConnection(ctx context.Context, id, connection string) error {
	if err := lockutil.RLock(ctx, &s.stateGate); err != nil {
		return err
	}
	defer s.stateGate.RUnlock()
	value, _ := s.operationLocks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	if err := lockutil.Lock(ctx, mu); err != nil {
		return err
	}
	defer mu.Unlock()
	m, ok := s.store.GetMapping(id)
	if !ok {
		return fmt.Errorf("映射不存在")
	}
	if err := s.singbox.CloseConnection(ctx, m.TunName, connection); err != nil {
		return err
	}
	s.operations.Audit("connection-close", id, "已关闭指定连接")
	return nil
}

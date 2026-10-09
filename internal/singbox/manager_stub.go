//go:build !linux

package singbox

import (
	"fmt"
	"sync"

	"github.com/lucaswren/s2l/internal/model"
)

// Manager 非 Linux 平台占位实现（本项目目标运行环境为 Ubuntu/Debian）
type Manager struct {
	binPath string
	mu      sync.Mutex
	pids    map[string]int
}

func NewManager(binPath string) *Manager {
	if binPath == "" {
		binPath = "sing-box"
	}
	return &Manager{binPath: binPath, pids: make(map[string]int)}
}

func (m *Manager) Start(node model.SocksNode, mapping model.TunnelMapping) error {
	if _, err := WriteConfig(node, mapping); err != nil {
		return err
	}
	return fmt.Errorf("sing-box process management is only supported on linux")
}

func (m *Manager) Stop(tunName string) error {
	_ = RemoveConfig(tunName)
	m.mu.Lock()
	delete(m.pids, tunName)
	m.mu.Unlock()
	return nil
}

func (m *Manager) Restart(node model.SocksNode, mapping model.TunnelMapping) error {
	_ = m.Stop(mapping.TunName)
	return m.Start(node, mapping)
}

func (m *Manager) PID(tunName string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pids[tunName]
}

func (m *Manager) PIDs() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int, len(m.pids))
	for k, v := range m.pids {
		out[k] = v
	}
	return out
}

func (m *Manager) IsRunning(tunName string) bool { return m.PID(tunName) > 0 }

func (m *Manager) StopAll() {}

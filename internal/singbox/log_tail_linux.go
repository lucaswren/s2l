//go:build linux

package singbox

func (m *Manager) LogTail(tunName string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.procs[tunName]; p != nil && p.log != nil {
		return p.log.Tail()
	}
	return ""
}

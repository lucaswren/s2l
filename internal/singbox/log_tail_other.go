//go:build !linux

package singbox

func (m *Manager) LogTail(string) string { return "" }

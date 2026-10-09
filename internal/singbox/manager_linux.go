//go:build linux

package singbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

// Manager 管理多个 sing-box 进程（每个 TUN / 映射一个实例）
type Manager struct {
	binPath string // sing-box 可执行文件路径，如 /usr/local/bin/sing-box

	mu    sync.Mutex
	procs map[string]*managedProc // key: tun_name
}

type managedProc struct {
	cmd    *exec.Cmd
	pid    int
	config string
	done   chan struct{}
}

// NewManager 创建进程管理器。binPath 为空时默认使用 PATH 中的 "sing-box"。
func NewManager(binPath string) *Manager {
	if binPath == "" {
		binPath = "sing-box"
	}
	return &Manager{
		binPath: binPath,
		procs:   make(map[string]*managedProc),
	}
}

// Start 为指定映射拉起 sing-box：写配置 → 启动进程 → 记录 PID
func (m *Manager) Start(node model.SocksNode, mapping model.TunnelMapping) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if mapping.TunName == "" {
		return fmt.Errorf("tun_name is required")
	}
	if p, ok := m.procs[mapping.TunName]; ok && p.cmd.Process != nil {
		if alive(p.pid) {
			return fmt.Errorf("sing-box for %s already running (pid=%d)", mapping.TunName, p.pid)
		}
		delete(m.procs, mapping.TunName)
	}

	cfgPath, err := WriteConfig(node, mapping)
	if err != nil {
		return err
	}

	logPath := filepath.Join("/tmp", fmt.Sprintf("s2l-%s.log", mapping.TunName))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = RemoveConfig(mapping.TunName)
		return fmt.Errorf("open sing-box log: %w", err)
	}

	cmd := exec.Command(m.binPath, "run", "-c", cfgPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		_ = RemoveConfig(mapping.TunName)
		return fmt.Errorf("start sing-box %s: %w", mapping.TunName, err)
	}
	// 子进程已继承 fd，父进程侧可关闭
	_ = logFile.Close()

	proc := &managedProc{
		cmd:    cmd,
		pid:    cmd.Process.Pid,
		config: cfgPath,
		done:   make(chan struct{}),
	}
	m.procs[mapping.TunName] = proc

	// 短暂观察，避免配置错误导致瞬间退出未被发现
	time.Sleep(500 * time.Millisecond)
	if !alive(cmd.Process.Pid) {
		_ = cmd.Wait()
		delete(m.procs, mapping.TunName)
		_ = RemoveConfig(mapping.TunName)
		return fmt.Errorf("sing-box for %s exited immediately, see %s and %s", mapping.TunName, cfgPath, logPath)
	}

	go m.reap(mapping.TunName, proc)
	return nil
}

// Stop 停止指定 TUN 对应的 sing-box，并清理配置文件
func (m *Manager) Stop(tunName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked(tunName)
}

func (m *Manager) stopLocked(tunName string) error {
	p, ok := m.procs[tunName]
	if !ok || p.cmd.Process == nil {
		_ = RemoveConfig(tunName)
		delete(m.procs, tunName)
		return nil
	}

	pid := p.pid
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
	}

	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = p.cmd.Process.Kill()
		<-p.done
	}

	delete(m.procs, tunName)
	_ = RemoveConfig(tunName)
	return nil
}

// Restart 先停后起
func (m *Manager) Restart(node model.SocksNode, mapping model.TunnelMapping) error {
	m.mu.Lock()
	_ = m.stopLocked(mapping.TunName)
	m.mu.Unlock()
	return m.Start(node, mapping)
}

// PID 返回指定 TUN 的 sing-box PID；未运行返回 0
func (m *Manager) PID(tunName string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.procs[tunName]
	if !ok || !alive(p.pid) {
		return 0
	}
	return p.pid
}

// PIDs 返回所有仍在运行的 tun_name -> pid
func (m *Manager) PIDs() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int, len(m.procs))
	for name, p := range m.procs {
		if alive(p.pid) {
			out[name] = p.pid
		}
	}
	return out
}

// IsRunning 判断指定 TUN 的 sing-box 是否在跑
func (m *Manager) IsRunning(tunName string) bool {
	return m.PID(tunName) > 0
}

// StopAll 停止全部实例（进程退出时调用）
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name := range m.procs {
		_ = m.stopLocked(name)
	}
}

func (m *Manager) reap(tunName string, p *managedProc) {
	_ = p.cmd.Wait()
	close(p.done)
	m.mu.Lock()
	defer m.mu.Unlock()
	if current, ok := m.procs[tunName]; ok && current == p {
		delete(m.procs, tunName)
	}
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

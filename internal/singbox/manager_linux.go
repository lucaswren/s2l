//go:build linux

package singbox

import (
	"context"
	"fmt"
	"github.com/lucaswren/s2l/internal/dnsproxy"
	"github.com/lucaswren/s2l/internal/lockutil"
	"github.com/lucaswren/s2l/internal/model"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type Manager struct {
	binPath string
	mu      sync.Mutex
	procs   map[string]*managedProc
	locks   sync.Map
}

func (m *Manager) ResolveDNS(ctx context.Context, name string, query []byte) ([]byte, error) {
	m.mu.Lock()
	p := m.procs[name]
	m.mu.Unlock()
	if p == nil || p.resolver == nil || !p.running() {
		return nil, fmt.Errorf("mapping DNS resolver is not running")
	}
	return p.resolver.Resolver.Resolve(ctx, query)
}

type managedProc struct {
	apiAddress string
	apiSecret  string
	cmd        *exec.Cmd
	pid        int
	done       chan struct{}
	resolver   *dnsproxy.Server
	log        *rotatingLog
	exits      uint64
}

func NewManager(binPath string) *Manager {
	if binPath == "" {
		binPath = "sing-box"
	}
	return &Manager{binPath: binPath, procs: make(map[string]*managedProc)}
}
func (m *Manager) lock(name string) func() {
	value, _ := m.locks.LoadOrStore(name, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
func (m *Manager) Start(node model.SocksNode, mapping model.TunnelMapping) error {
	unlock := m.lock(mapping.TunName)
	defer unlock()
	return m.start(node, mapping)
}
func (m *Manager) start(node model.SocksNode, mapping model.TunnelMapping) error {
	if mapping.TunName == "" {
		return fmt.Errorf("tun_name is required")
	}
	m.mu.Lock()
	previous := m.procs[mapping.TunName]
	m.mu.Unlock()
	if previous != nil && previous.running() {
		return fmt.Errorf("sing-box for %s already running", mapping.TunName)
	}
	resolver, err := dnsproxy.Start(node, mapping.EffectiveDNSMode(), mapping.EffectiveUDPPolicy())
	if err != nil {
		return fmt.Errorf("start DNS resolver: %w", err)
	}
	started := false
	defer func() {
		if !started {
			resolver.Close()
		}
	}()
	cfgPath, err := WriteConfig(node, mapping, resolver.Address())
	if err != nil {
		return err
	}
	apiAddress, apiSecret, err := prepareAPI(cfgPath)
	if err != nil {
		_ = RemoveConfig(mapping.TunName)
		return err
	}
	logPath := filepath.Join(RuntimeDir(), fmt.Sprintf("s2l-%s.log", mapping.TunName))
	writer, err := openDebugLog(logPath)
	if err != nil {
		_ = RemoveConfig(mapping.TunName)
		return fmt.Errorf("open sing-box log: %w", err)
	}
	cmd := exec.Command(m.binPath, "run", "-c", cfgPath)
	cmd.WaitDelay = time.Second
	cmd.Stdout = writer
	cmd.Stderr = writer
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		writer.Close()
		_ = RemoveConfig(mapping.TunName)
		return fmt.Errorf("start sing-box: %w", err)
	}
	p := &managedProc{apiAddress: apiAddress, apiSecret: apiSecret, cmd: cmd, pid: cmd.Process.Pid, done: make(chan struct{}), resolver: resolver, log: writer}
	if previous != nil {
		p.exits = previous.exits + 1
	}
	m.mu.Lock()
	m.procs[mapping.TunName] = p
	m.mu.Unlock()
	started = true
	go m.reap(mapping.TunName, p)
	select {
	case <-p.done:
		_ = RemoveConfig(mapping.TunName)
		return fmt.Errorf("sing-box exited immediately; inspect %s", logPath)
	case <-time.After(500 * time.Millisecond):
		return nil
	}
}
func (m *Manager) Stop(name string) error {
	unlock := m.lock(name)
	defer unlock()
	return m.stop(name)
}
func (m *Manager) stop(name string) error {
	m.mu.Lock()
	p := m.procs[name]
	m.mu.Unlock()
	if p != nil && p.cmd != nil && p.cmd.Process != nil && p.running() {
		if err := syscall.Kill(-p.pid, syscall.SIGTERM); err != nil {
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-p.pid, syscall.SIGKILL)
			_ = p.cmd.Process.Kill()
			select {
			case <-p.done:
			case <-time.After(2 * time.Second):
				return fmt.Errorf("sing-box for %s did not terminate", name)
			}
		}
	}
	if p != nil {
		if p.resolver != nil {
			p.resolver.Close()
		}
		if p.log != nil {
			_ = p.log.Close()
		}
	}
	return RemoveConfig(name)
}
func (m *Manager) Restart(node model.SocksNode, mapping model.TunnelMapping) error {
	unlock := m.lock(mapping.TunName)
	defer unlock()
	if err := m.stop(mapping.TunName); err != nil {
		return err
	}
	return m.start(node, mapping)
}
func (m *Manager) PID(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.procs[name]
	if p == nil || !p.running() {
		return 0
	}
	return p.pid
}
func (m *Manager) PIDs() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int)
	for name, p := range m.procs {
		if p.running() {
			out[name] = p.pid
		}
	}
	return out
}
func (m *Manager) IsRunning(name string) bool { return m.PID(name) > 0 }
func (m *Manager) StopAll() {
	m.mu.Lock()
	names := make([]string, 0, len(m.procs))
	for name := range m.procs {
		names = append(names, name)
	}
	m.mu.Unlock()
	for _, name := range names {
		_ = m.Stop(name)
	}
}
func (m *Manager) ResolverStatus(name string) model.ResolverStatus {
	m.mu.Lock()
	p := m.procs[name]
	m.mu.Unlock()
	if p == nil || p.resolver == nil {
		return model.ResolverStatus{}
	}
	return p.resolver.Resolver.Status()
}
func (m *Manager) ProcessStatus(name string) model.ProcessStatus {
	m.mu.Lock()
	p := m.procs[name]
	m.mu.Unlock()
	if p == nil {
		return model.ProcessStatus{}
	}
	var s model.ProcessStatus
	if p.log != nil {
		s = p.log.Status()
	}
	s.Exits = p.exits
	if !p.running() {
		s.Exits++
	}
	return s
}

// Snapshot binds PID, resolver and counters to the same managed process.
// Reading those through separate lookups can mix generations during Restart.
func (m *Manager) RuntimeSnapshot() map[string]RuntimeStats {
	m.mu.Lock()
	procs := make(map[string]*managedProc, len(m.procs))
	for name, p := range m.procs {
		procs[name] = p
	}
	m.mu.Unlock()
	out := make(map[string]RuntimeStats, len(procs))
	for name, p := range procs {
		stats := RuntimeStats{}
		alive := p.running()
		if alive {
			stats.PID = p.pid
		}
		if p.resolver != nil {
			stats.Resolver = p.resolver.Resolver.Status()
		}
		if p.log != nil {
			stats.Process = p.log.Status()
		}
		stats.Process.Exits = p.exits
		if !alive {
			stats.Process.Exits++
		}
		out[name] = stats
	}
	return out
}
func (m *Manager) reap(_ string, p *managedProc) {
	_ = p.cmd.Wait()
	close(p.done)
	if p.resolver != nil {
		p.resolver.Close()
	}
	if p.log != nil {
		_ = p.log.Close()
	}
}
func (p *managedProc) running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (m *Manager) Connections(ctx context.Context, name string) (Connections, error) {
	value, _ := m.locks.LoadOrStore(name, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	if err := lockutil.Lock(ctx, mu); err != nil {
		return Connections{}, err
	}
	defer mu.Unlock()
	m.mu.Lock()
	p := m.procs[name]
	m.mu.Unlock()
	if p == nil || !p.running() {
		return Connections{}, fmt.Errorf("映射进程未运行")
	}
	return readConnections(ctx, p.apiAddress, p.apiSecret)
}
func (m *Manager) CloseConnection(ctx context.Context, name, id string) error {
	value, _ := m.locks.LoadOrStore(name, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	if err := lockutil.Lock(ctx, mu); err != nil {
		return err
	}
	defer mu.Unlock()
	m.mu.Lock()
	p := m.procs[name]
	m.mu.Unlock()
	if p == nil || !p.running() {
		return fmt.Errorf("映射进程未运行")
	}
	return closeConnection(ctx, p.apiAddress, p.apiSecret, id)
}

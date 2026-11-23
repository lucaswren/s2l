//go:build linux

package singbox

import (
	"context"
	"fmt"
	"github.com/lucaswren/s2l/internal/dnsproxy"
	"github.com/lucaswren/s2l/internal/lockutil"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/speedtest"
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
	return p.resolver.Query(ctx, query)
}

func (m *Manager) DNSProbe(name string) (func(context.Context, []byte) ([]byte, error), func() bool, error) {
	m.mu.Lock()
	p := m.procs[name]
	m.mu.Unlock()
	if p == nil || p.resolver == nil || !p.running() {
		return nil, nil, fmt.Errorf("mapping DNS resolver is not running")
	}
	valid := func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.procs[name] == p && p.running() }
	return p.resolver.Query, valid, nil
}

type managedProc struct {
	apiAddress           string
	apiSecret            string
	cmd                  *exec.Cmd
	pid                  int
	done                 chan struct{}
	resolver             *dnsproxy.Server
	log                  *processLog
	lifecycleMu          sync.Mutex
	stopRequested        bool
	forcedStop           bool
	plannedExit          bool
	exits                uint64
	restarts             uint64
	lastUnexpectedExitAt time.Time
	lastUnexpectedExit   string
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
	if err := speedtest.CheckWANEgress(node); err != nil {
		return err
	}
	if mapping.TunName == "" {
		return fmt.Errorf("tun_name is required")
	}
	m.mu.Lock()
	previous := m.procs[mapping.TunName]
	m.mu.Unlock()
	if previous != nil && previous.running() {
		return fmt.Errorf("sing-box for %s already running", mapping.TunName)
	}
	resolver, err := dnsproxy.Start(node, mapping.EffectiveDNSOptions())
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
	var apiAddress, apiSecret string
	if mapping.ObserveConnections {
		apiAddress, apiSecret, err = prepareAPI(cfgPath)
	}
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
		p.inheritLifecycle(previous)
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
		p.lifecycleMu.Lock()
		err := syscall.Kill(-p.pid, syscall.SIGTERM)
		if err != nil {
			err = p.cmd.Process.Signal(syscall.SIGTERM)
		}
		if err == nil {
			p.stopRequested = true
		}
		p.lifecycleMu.Unlock()
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			p.lifecycleMu.Lock()
			err := syscall.Kill(-p.pid, syscall.SIGKILL)
			if err != nil {
				err = p.cmd.Process.Kill()
			}
			p.forcedStop = err == nil
			p.lifecycleMu.Unlock()
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
	return p.processStatus()
}

func (p *managedProc) inheritLifecycle(previous *managedProc) {
	previous.lifecycleMu.Lock()
	defer previous.lifecycleMu.Unlock()
	p.exits = previous.exits
	p.restarts = previous.restarts
	p.lastUnexpectedExitAt = previous.lastUnexpectedExitAt
	p.lastUnexpectedExit = previous.lastUnexpectedExit
	if previous.plannedExit {
		p.restarts++
	}
}

func (p *managedProc) processStatus() model.ProcessStatus {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	var status model.ProcessStatus
	if p.log != nil {
		status = p.log.Status()
	}
	status.Exits = p.exits
	status.Restarts = p.restarts
	if !p.lastUnexpectedExitAt.IsZero() {
		at := p.lastUnexpectedExitAt
		status.LastUnexpectedExitAt = &at
	}
	status.LastUnexpectedExit = p.lastUnexpectedExit
	return status
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
		stats.Process = p.processStatus()
		out[name] = stats
	}
	return out
}
func (m *Manager) reap(_ string, p *managedProc) {
	err := p.cmd.Wait()
	p.lifecycleMu.Lock()
	if p.cmd.ProcessState != nil {
		waitStatus, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus)
		p.plannedExit = (p.stopRequested && (p.cmd.ProcessState.Success() ||
			(ok && waitStatus.Signal() == syscall.SIGTERM))) ||
			(p.forcedStop && ok && waitStatus.Signal() == syscall.SIGKILL)
	}
	if !p.plannedExit {
		p.exits++
		p.lastUnexpectedExitAt = time.Now()
		switch {
		case p.cmd.ProcessState != nil:
			p.lastUnexpectedExit = p.cmd.ProcessState.String()
		case err != nil:
			p.lastUnexpectedExit = "wait failed"
		default:
			p.lastUnexpectedExit = "process ended"
		}
	}
	close(p.done)
	p.lifecycleMu.Unlock()
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
	if p.apiAddress == "" {
		return Connections{}, fmt.Errorf("connection observation is disabled")
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
	if p.apiAddress == "" {
		return fmt.Errorf("connection observation is disabled")
	}
	return closeConnection(ctx, p.apiAddress, p.apiSecret, id)
}

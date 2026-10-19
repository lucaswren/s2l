package service

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/debuglog"
	"github.com/lucaswren/s2l/internal/l2tp"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/network"
	"github.com/lucaswren/s2l/internal/singbox"
	"github.com/lucaswren/s2l/internal/store"
)

// MappingService 编排「sing-box + 网络规则 + L2TP」整条链路
type MappingService struct {
	store           *store.Store
	singbox         *singbox.Manager
	net             *network.Controller
	l2tp            *l2tp.Manager
	startedAt       time.Time
	shuttingDown    bool
	startupComplete bool
	probes          nodeProbeCache

	mu             sync.Mutex
	stateGate      sync.RWMutex // state edits exclude lifecycle work; independent mappings share read access
	operationLocks sync.Map
	lifecycleMu    sync.Mutex // xl2tpd configuration and persisted active set form one transaction
	inflight       map[string]bool
	health         map[string]*healthRecord
}

func NewMappingService(
	st *store.Store,
	sb *singbox.Manager,
	netCtrl *network.Controller,
	l2tpMgr *l2tp.Manager,
) *MappingService {
	return &MappingService{
		store:     st,
		singbox:   sb,
		net:       netCtrl,
		l2tp:      l2tpMgr,
		startedAt: time.Now(),
		health:    make(map[string]*healthRecord),
		inflight:  make(map[string]bool),
	}
}

// EnsureL2TP 启动时修复 xl2tpd.conf（清除历史 # 注释损坏配置）并尝试拉起服务
func (s *MappingService) EnsureL2TP() {
	var active []model.TunnelMapping
	for _, m := range s.store.ListMappings() {
		if m.Status == model.StatusRunning {
			active = append(active, m)
		}
	}
	if err := s.l2tp.SyncConfOnly(active); err != nil {
		debuglog.Default.Printf("l2tp ensure warning: %v", err)
		return
	}
	debuglog.Default.Printf("l2tp: xl2tpd.conf synced")
}

// RestoreRunning 开机/进程重启后，按 state.json 中 status=running 的映射自动拉起
func (s *MappingService) RestoreRunning() {
	var ids []string
	for _, m := range s.store.ListMappings() {
		if m.Status == model.StatusRunning {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		debuglog.Default.Printf("restore: no running mappings to restore")
		return
	}
	debuglog.Default.Printf("restore: bringing up %d mapping(s)...", len(ids))
	for _, id := range ids {
		restored, err := s.restoreMapping(id)
		if !restored {
			continue
		}
		if err != nil {
			debuglog.Default.Printf("restore: mapping %s failed: %v", id, err)
			continue
		}
		debuglog.Default.Printf("restore: mapping %s ok", id)
	}
}

func (s *MappingService) restoreMapping(id string) (bool, error) {
	s.stateGate.RLock()
	defer s.stateGate.RUnlock()
	unlock := s.lockMapping(id)
	defer unlock()
	s.mu.Lock()
	shuttingDown := s.shuttingDown
	alreadyStarted := s.health[id] != nil
	s.mu.Unlock()
	m, ok := s.store.GetMapping(id)
	if !ok || m.Status != model.StatusRunning || shuttingDown || alreadyStarted {
		return false, nil
	}
	err := s.startLocked(id)
	if err != nil && network.ValidateMapping(m) == nil && m.L2TPUser != "" && m.L2TPPass != "" {
		if _, ok := s.store.GetNode(m.NodeID); ok {
			err = errors.Join(err, s.scheduleStartupRetry(m, err))
		}
	}
	return true, err
}

// SynchronizeState coordinates API state edits with startup and recovery.
// The callback must not call lifecycle methods, which acquire the same lock.
func (s *MappingService) SynchronizeState(change func()) {
	s.stateGate.Lock()
	defer s.stateGate.Unlock()
	change()
}

// Start 启动一条映射：L2TP → sing-box → 策略路由/iptables
func (s *MappingService) Start(id string) error {
	s.stateGate.RLock()
	defer s.stateGate.RUnlock()
	unlock := s.lockMapping(id)
	defer unlock()
	s.mu.Lock()
	shuttingDown := s.shuttingDown
	s.mu.Unlock()
	if shuttingDown {
		return fmt.Errorf("service is shutting down")
	}
	return s.startLocked(id)
}

func (s *MappingService) startLocked(id string) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	m, ok := s.store.GetMapping(id)
	if !ok {
		return fmt.Errorf("mapping %s not found", id)
	}
	if err := network.ValidateMapping(m); err != nil {
		cause := fmt.Errorf("invalid mapping: %w", err)
		return errors.Join(cause, s.store.UpdateMappingStatus(id, model.StatusError, cause.Error()))
	}
	node, ok := s.store.GetNode(m.NodeID)
	if !ok {
		cause := fmt.Errorf("node %s not found", m.NodeID)
		return errors.Join(cause, s.store.UpdateMappingStatus(id, model.StatusError, cause.Error()))
	}
	if m.L2TPUser == "" || m.L2TPPass == "" {
		cause := fmt.Errorf("l2tp_user/l2tp_pass required")
		return errors.Join(cause, s.store.UpdateMappingStatus(id, model.StatusError, cause.Error()))
	}
	s.mu.Lock()
	knownHealthy := s.health[id] != nil && s.health[id].LocalState == "healthy"
	s.mu.Unlock()
	if m.Status == model.StatusRunning && knownHealthy && s.singbox.IsRunning(m.TunName) && network.WaitInterface(m.TunName, 100*time.Millisecond) == nil {
		return nil
	}

	s.mu.Lock()
	delete(s.health, id)
	s.mu.Unlock()

	// 幂等：先清残留（适配开机恢复 / 重复 start）
	if err := s.cleanupRuntime(m); err != nil {
		_ = s.store.UpdateMappingStatus(id, model.StatusError, "pre-start cleanup failed; enable Debug, reproduce and export logs")
		return fmt.Errorf("pre-start cleanup: %w", err)
	}

	active := s.activeAfter(m, true)
	previousActive := s.activeAfter(m, false)
	if hardErr, reloadErr := s.l2tp.ApplyStart(m, active); hardErr != nil {
		_ = s.store.UpdateMappingStatus(id, model.StatusError, hardErr.Error())
		return hardErr
	} else if reloadErr != nil {
		return s.failStart(m, previousActive, fmt.Errorf("xl2tpd reload: %w", reloadErr))
	}

	if err := s.singbox.Start(node, m); err != nil {
		return s.failStart(m, previousActive, fmt.Errorf("sing-box: %w", err))
	}
	// TUN 由 sing-box 异步创建，必须等网卡就绪再加策略路由
	if err := network.WaitInterface(m.TunName, 15*time.Second); err != nil {
		_ = s.singbox.Stop(m.TunName)
		msg := fmt.Sprintf("%v; enable Debug and export logs from settings", err)
		return s.failStart(m, previousActive, fmt.Errorf("tun: %s", msg))
	}
	if err := s.net.Setup(m); err != nil {
		_ = s.singbox.Stop(m.TunName)
		return s.failStart(m, previousActive, fmt.Errorf("network: %w", err))
	}

	if err := s.net.Verify(m); err != nil {
		return s.failStart(m, previousActive, errors.Join(err, s.cleanupRuntime(m)))
	}
	if err := s.store.UpdateMappingStatus(id, model.StatusRunning, ""); err != nil {
		cleanupErr := s.cleanupRuntime(m)
		rollbackErr := s.l2tp.ApplyStop(m.ID, previousActive)
		return errors.Join(fmt.Errorf("persist running status: %w", err), cleanupErr, rollbackErr)
	}
	s.mu.Lock()
	s.health[id] = &healthRecord{UpstreamHealth: model.UpstreamHealth{State: "checking", TunName: m.TunName, LocalState: "healthy", LocalCheckedAt: time.Now()}}
	s.mu.Unlock()
	return nil
}

// Stop 停止映射并清理网络 / 进程 / L2TP
func (s *MappingService) Stop(id string) error {
	s.stateGate.RLock()
	defer s.stateGate.RUnlock()
	unlock := s.lockMapping(id)
	defer unlock()
	return s.stopLocked(id)
}

func (s *MappingService) lockMapping(id string) func() {
	value, _ := s.operationLocks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *MappingService) DeleteMapping(id string) error {
	s.stateGate.Lock()
	defer s.stateGate.Unlock()
	unlock := s.lockMapping(id)
	defer unlock()
	if _, ok := s.store.GetMapping(id); !ok {
		return fmt.Errorf("mapping not found")
	}
	if err := s.stopLocked(id); err != nil {
		return err
	}
	return s.store.DeleteMapping(id)
}

func (s *MappingService) stopLocked(id string) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	m, ok := s.store.GetMapping(id)
	if !ok {
		return fmt.Errorf("mapping %s not found", id)
	}

	s.mu.Lock()
	delete(s.health, id)
	s.mu.Unlock()
	cleanupErr := s.cleanupRuntime(m)
	active := s.activeAfter(m, false)
	l2tpErr := s.l2tp.ApplyStop(m.ID, active)
	if cleanupErr != nil || l2tpErr != nil {
		msg := errors.Join(cleanupErr, l2tpErr).Error()
		_ = s.store.UpdateMappingStatus(id, model.StatusError, msg)
		return fmt.Errorf("stop mapping: %s", msg)
	}

	return s.store.UpdateMappingStatus(id, model.StatusStopped, "")
}

// Shutdown 优雅退出：拆掉运行时资源，但保留 status=running，以便下次开机恢复
func (s *MappingService) Shutdown() {
	s.stateGate.Lock()
	defer s.stateGate.Unlock()
	s.mu.Lock()
	s.shuttingDown = true
	s.mu.Unlock()
	for _, m := range s.store.ListMappings() {
		if m.Status == model.StatusRunning {
			_ = s.cleanupRuntime(m)
		}
	}
	if err := s.net.TeardownAll(); err != nil {
		debuglog.Default.Printf("network shutdown cleanup warning: %v", err)
	}
	s.singbox.StopAll()
}

// Status 汇总运行状态与 TUN 流量
func (s *MappingService) Status() model.SystemStatus {
	s.mu.Lock()
	mappings := s.store.ListMappings()
	health := make(map[string]model.UpstreamHealth)
	for id, h := range s.health {
		health[id] = h.UpstreamHealth
		value := health[id]
		value.Recovery = h.recovery.status(h.localNext, h.recoveryFailures)
		health[id] = value
		if h.LocalState != "" && h.LocalState != "healthy" {
			value := health[id]
			value.State = h.LocalState
			value.Error = h.RecoveryError
			health[id] = value
		}
	}
	s.mu.Unlock()
	runtime := s.singbox.RuntimeSnapshot()
	pids := make(map[string]int)
	for name, stats := range runtime {
		if stats.PID > 0 {
			pids[name] = stats.PID
		}
	}
	traffic := make([]model.TunTraffic, 0, len(mappings))
	for _, m := range mappings {
		if m.Status != model.StatusRunning {
			continue
		}
		t, err := network.ReadTunTraffic(m.TunName)
		if err != nil || s.singbox.PID(m.TunName) != runtime[m.TunName].PID {
			t = model.TunTraffic{TunName: m.TunName, Error: "interface statistics unavailable"}
		}
		if h, ok := health[m.ID]; ok {
			h.Resolver = runtime[m.TunName].Resolver
			h.Process = runtime[m.TunName].Process
			health[m.ID] = h
		}
		traffic = append(traffic, t)
	}
	return model.SystemStatus{
		Uptime:         time.Since(s.startedAt),
		Mappings:       mappings,
		TunTraffic:     traffic,
		SingboxPIDs:    pids,
		UpstreamHealth: health,
	}
}

func (s *MappingService) cleanupRuntime(m model.TunnelMapping) error {
	return errors.Join(s.net.Teardown(m), s.singbox.Stop(m.TunName))
}

func (s *MappingService) failStart(m model.TunnelMapping, previous []model.TunnelMapping, cause error) error {
	rollbackErr := s.l2tp.ApplyStop(m.ID, previous)
	if rollbackErr != nil {
		cause = errors.Join(cause, fmt.Errorf("L2TP rollback: %w", rollbackErr))
	}
	_ = s.store.UpdateMappingStatus(m.ID, model.StatusError, cause.Error())
	return cause
}

// activeAfter 计算启停后应写入 xl2tpd 的活跃映射列表
func (s *MappingService) activeAfter(target model.TunnelMapping, starting bool) []model.TunnelMapping {
	var out []model.TunnelMapping
	for _, m := range s.store.ListMappings() {
		if m.ID == target.ID {
			if starting {
				out = append(out, target)
			}
			continue
		}
		if m.Status == model.StatusRunning {
			out = append(out, m)
		}
	}
	return out
}

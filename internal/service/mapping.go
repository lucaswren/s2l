package service

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/l2tp"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/network"
	"github.com/lucaswren/s2l/internal/singbox"
	"github.com/lucaswren/s2l/internal/store"
)

// MappingService 编排「sing-box + 网络规则 + L2TP」整条链路
type MappingService struct {
	store     *store.Store
	singbox   *singbox.Manager
	net       *network.Controller
	l2tp      *l2tp.Manager
	startedAt time.Time

	mu sync.Mutex
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
		log.Printf("l2tp ensure warning: %v", err)
		return
	}
	log.Println("l2tp: xl2tpd.conf synced")
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
		log.Println("restore: no running mappings to restore")
		return
	}
	log.Printf("restore: bringing up %d mapping(s)...", len(ids))
	for _, id := range ids {
		if err := s.Start(id); err != nil {
			log.Printf("restore: mapping %s failed: %v", id, err)
			continue
		}
		log.Printf("restore: mapping %s ok", id)
	}
}

// Start 启动一条映射：L2TP → sing-box → 策略路由/iptables
func (s *MappingService) Start(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startLocked(id)
}

func (s *MappingService) startLocked(id string) error {
	m, ok := s.store.GetMapping(id)
	if !ok {
		return fmt.Errorf("mapping %s not found", id)
	}
	if err := network.ValidateMapping(m); err != nil {
		return fmt.Errorf("invalid mapping: %w", err)
	}
	node, ok := s.store.GetNode(m.NodeID)
	if !ok {
		return fmt.Errorf("node %s not found", m.NodeID)
	}
	if m.L2TPUser == "" || m.L2TPPass == "" {
		return fmt.Errorf("l2tp_user/l2tp_pass required")
	}

	// 幂等：先清残留（适配开机恢复 / 重复 start）
	if err := s.cleanupRuntime(m); err != nil {
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
		msg := fmt.Sprintf("%v; check /tmp/s2l-%s.log", err, m.TunName)
		return s.failStart(m, previousActive, fmt.Errorf("tun: %s", msg))
	}
	if err := s.net.Setup(m); err != nil {
		_ = s.singbox.Stop(m.TunName)
		return s.failStart(m, previousActive, fmt.Errorf("network: %w", err))
	}

	if err := s.store.UpdateMappingStatus(id, model.StatusRunning, ""); err != nil {
		cleanupErr := s.cleanupRuntime(m)
		rollbackErr := s.l2tp.ApplyStop(m.ID, previousActive)
		return errors.Join(fmt.Errorf("persist running status: %w", err), cleanupErr, rollbackErr)
	}
	return nil
}

// Stop 停止映射并清理网络 / 进程 / L2TP
func (s *MappingService) Stop(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.store.GetMapping(id)
	if !ok {
		return fmt.Errorf("mapping %s not found", id)
	}

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
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.store.ListMappings() {
		if m.Status == model.StatusRunning {
			_ = s.cleanupRuntime(m)
		}
	}
	if err := s.net.TeardownAll(); err != nil {
		log.Printf("network shutdown cleanup warning: %v", err)
	}
	s.singbox.StopAll()
}

// Status 汇总运行状态与 TUN 流量
func (s *MappingService) Status() model.SystemStatus {
	mappings := s.store.ListMappings()
	traffic := make([]model.TunTraffic, 0, len(mappings))
	for _, m := range mappings {
		if m.Status != model.StatusRunning {
			continue
		}
		t, err := network.ReadTunTraffic(m.TunName)
		if err != nil {
			t = model.TunTraffic{TunName: m.TunName}
		}
		traffic = append(traffic, t)
	}
	return model.SystemStatus{
		Uptime:      time.Since(s.startedAt),
		Mappings:    mappings,
		TunTraffic:  traffic,
		SingboxPIDs: s.singbox.PIDs(),
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

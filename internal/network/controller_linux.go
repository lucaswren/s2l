//go:build linux

package network

import (
	"errors"
	"fmt"
	"sync"

	"github.com/lucaswren/s2l/internal/model"
)

// Controller 编排单条映射的策略路由 + iptables 生命周期
type Controller struct {
	mu       sync.Mutex
	active   map[string]model.TunnelMapping // key: mapping.ID 或 tun_name
	tcpmssOn bool
}

func NewController() *Controller {
	return &Controller{
		active: make(map[string]model.TunnelMapping),
	}
}

func mappingKey(m model.TunnelMapping) string {
	if m.ID != "" {
		return m.ID
	}
	return m.TunName
}

// Setup 在 sing-box TUN 已就绪后调用：开转发 → 策略路由 → 防火墙 → TCPMSS
func (c *Controller) Setup(m model.TunnelMapping) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := validateMapping(m); err != nil {
		return err
	}
	if err := ensureIPForward(); err != nil {
		return err
	}
	_ = TuneInterface(m.TunName, 1400)
	if err := AddPolicyRoute(m); err != nil {
		return fmt.Errorf("policy route: %w", err)
	}
	if err := AddFirewall(m); err != nil {
		routeErr := DelPolicyRoute(m)
		return errors.Join(fmt.Errorf("firewall: %w", err), routeErr)
	}
	if err := EnsureTCPMSS(); err != nil {
		return errors.Join(fmt.Errorf("tcpmss: %w", err), DelFirewall(m), DelPolicyRoute(m))
	}

	c.active[mappingKey(m)] = m
	c.tcpmssOn = true
	return nil
}

// Teardown 停止映射时清理网络规则
func (c *Controller) Teardown(m model.TunnelMapping) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	firewallErr := DelFirewall(m)
	routeErr := DelPolicyRoute(m)
	delete(c.active, mappingKey(m))

	if len(c.active) == 0 && c.tcpmssOn {
		RemoveTCPMSS()
		c.tcpmssOn = false
	}
	return errors.Join(firewallErr, routeErr)
}

// TeardownAll 进程退出时清理全部规则
func (c *Controller) TeardownAll() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var errs []error
	for _, m := range c.active {
		errs = append(errs, DelFirewall(m), DelPolicyRoute(m))
	}
	c.active = make(map[string]model.TunnelMapping)
	if c.tcpmssOn {
		RemoveTCPMSS()
		c.tcpmssOn = false
	}
	return errors.Join(errs...)
}

// ActiveCount 当前已 Setup 的映射数量
func (c *Controller) ActiveCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active)
}

func ensureIPForward() error {
	// NAT/转发依赖内核转发；关闭 rp_filter 避免 PPP↔TUN 非对称路径被丢
	if err := run("sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return err
	}
	runAllowFail("sysctl", "-w", "net.ipv4.conf.all.rp_filter=0")
	runAllowFail("sysctl", "-w", "net.ipv4.conf.default.rp_filter=0")
	return nil
}

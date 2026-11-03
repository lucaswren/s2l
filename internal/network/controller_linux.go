//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

// Controller 编排单条映射的策略路由 + iptables 生命周期
type Controller struct {
	management   model.ManagementAccess
	mu           sync.Mutex
	active       map[string]model.TunnelMapping // key: mapping.ID 或 tun_name
	sessionGuard bool
	admitted     map[string]model.PPPSession
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

// Setup 在 sing-box TUN 已就绪后准备转发路径，最后放行映射流量。
func (c *Controller) Setup(m model.TunnelMapping) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := validateMapping(m); err != nil {
		return err
	}
	if err := ensureGuard(c.management); err != nil {
		return err
	}
	if err := ensureIPForward(); err != nil {
		return err
	}
	if err := TuneInterface(m.TunName, m.EffectiveMTU(), m.EffectiveQueueLength()); err != nil {
		return fmt.Errorf("tune interface: %w", err)
	}
	if err := AddPolicyRoute(m); err != nil {
		return fmt.Errorf("policy route: %w", err)
	}
	if owner, ok := c.admitted[m.ID]; ok {
		iface, err := net.InterfaceByName(owner.Interface)
		if err != nil || iface.Index != owner.Index {
			delete(c.admitted, m.ID)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := pinSessionReturn(ctx, m, owner)
			cancel()
			if err != nil {
				return errors.Join(fmt.Errorf("restore PPP return route: %w", err), DelPolicyRoute(m))
			}
		}
	}
	if err := applyBandwidth(m); err != nil {
		return errors.Join(fmt.Errorf("bandwidth: %w", err), DelFirewall(m), DelPolicyRoute(m))
	}
	if err := TunePeers(m); err != nil {
		return errors.Join(fmt.Errorf("tune connected PPP interfaces: %w", err), DelFirewall(m), DelPolicyRoute(m))
	}
	if err := AddFirewall(m); err != nil {
		return errors.Join(fmt.Errorf("firewall: %w", err), DelPolicyRoute(m))
	}
	c.active[mappingKey(m)] = m
	return nil
}

// Teardown 停止映射时清理网络规则
func (c *Controller) Teardown(m model.TunnelMapping) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	firewallErr := DelFirewall(m)
	routeErr := DelPolicyRoute(m)
	delete(c.active, mappingKey(m))
	delete(c.admitted, m.ID)

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
	if c.sessionGuard {
		errs = append(errs, replaceSessionGuard(context.Background(), nil))
		c.admitted = nil
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
	for _, key := range []string{"net.ipv4.conf.all.rp_filter=0", "net.ipv4.conf.default.rp_filter=0"} {
		if err := run("sysctl", "-w", key); err != nil {
			return err
		}
	}
	return nil
}

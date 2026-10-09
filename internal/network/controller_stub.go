//go:build !linux

package network

import (
	"fmt"
	"sync"

	"github.com/lucaswren/s2l/internal/model"
)

// Controller 非 Linux 占位实现
type Controller struct {
	mu     sync.Mutex
	active map[string]model.TunnelMapping
}

func NewController() *Controller {
	return &Controller{active: make(map[string]model.TunnelMapping)}
}

func (c *Controller) Setup(m model.TunnelMapping) error {
	return fmt.Errorf("network controller (ip rule/iptables) is only supported on linux")
}

func (c *Controller) Teardown(m model.TunnelMapping) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := m.ID
	if key == "" {
		key = m.TunName
	}
	delete(c.active, key)
	return nil
}

func (c *Controller) TeardownAll() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = make(map[string]model.TunnelMapping)
	return nil
}

func (c *Controller) ActiveCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active)
}

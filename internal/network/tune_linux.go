//go:build linux

package network

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
)

// TuneInterface 优化 TUN/PPP 口队列与 MTU，减轻分片与延迟
func TuneInterface(name string, mtu int, queueLength ...int) error {
	if name == "" {
		return fmt.Errorf("interface name empty")
	}
	if mtu <= 0 {
		mtu = 1400
	}
	if err := run("ip", "link", "set", "dev", name, "mtu", fmt.Sprintf("%d", mtu)); err != nil {
		return fmt.Errorf("set interface MTU: %w", err)
	}
	queue := 1000
	if len(queueLength) > 0 {
		queue = queueLength[0]
	}
	if err := run("ip", "link", "set", "dev", name, "txqueuelen", fmt.Sprint(queue)); err != nil {
		return fmt.Errorf("set interface queue length: %w", err)
	}
	if err := run("sysctl", "-w", "net/ipv4/conf/"+name+"/rp_filter=0"); err != nil {
		return err
	}
	return run("ip", "link", "set", "dev", name, "up")
}

// Connected PPP interfaces predate default sysctl changes; tune them explicitly.
func TunePeers(m model.TunnelMapping) error {
	rows, err := ReadPeers([]model.TunnelMapping{m})
	if err != nil {
		return err
	}
	return TunePeerSnapshot(m, rows)
}

func TunePeerSnapshot(m model.TunnelMapping, rows []model.PPPSession) error {
	for _, p := range rows {
		if p.MappingID != m.ID {
			continue
		}
		rp, err := os.ReadFile(filepath.Join("/proc/sys/net/ipv4/conf", p.Interface, "rp_filter"))
		if err == nil && strings.TrimSpace(string(rp)) == "0" && p.MTU == m.EffectiveMTU() && p.QueueLength == m.EffectiveQueueLength() {
			continue
		}
		if err := TuneInterface(p.Interface, m.EffectiveMTU(), m.EffectiveQueueLength()); err != nil {
			return err
		}
	}
	return nil
}

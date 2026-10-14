//go:build linux

package network

import (
	"encoding/json"
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	_, subnet, err := net.ParseCIDR(m.Subnet)
	if err != nil {
		return err
	}
	data, err := output("ip", "-j", "address", "show")
	if err != nil {
		return err
	}
	var interfaces []struct {
		Name        string `json:"ifname"`
		MTU         int    `json:"mtu"`
		QueueLength int    `json:"txqlen"`
		Addresses   []struct {
			Local string `json:"local"`
			Peer  string `json:"peer"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal([]byte(data), &interfaces); err != nil {
		return err
	}
	for _, iface := range interfaces {
		if !strings.HasPrefix(iface.Name, "ppp") {
			continue
		}
		for _, address := range iface.Addresses {
			peer := strings.Split(address.Peer, "/")[0]
			ip := net.ParseIP(peer)
			if ip == nil {
				ip = net.ParseIP(address.Local)
			}
			if ip != nil && subnet.Contains(ip) {
				rp, readErr := os.ReadFile(filepath.Join("/proc/sys/net/ipv4/conf", iface.Name, "rp_filter"))
				if readErr == nil && strings.TrimSpace(string(rp)) == "0" && iface.MTU == m.EffectiveMTU() && iface.QueueLength == m.EffectiveQueueLength() {
					break
				}
				if err := TuneInterface(iface.Name, m.EffectiveMTU(), m.EffectiveQueueLength()); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

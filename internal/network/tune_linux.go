//go:build linux

package network

import "fmt"

// TuneInterface 优化 TUN/PPP 口队列与 MTU，减轻分片与延迟
func TuneInterface(name string, mtu int) error {
	if name == "" {
		return fmt.Errorf("interface name empty")
	}
	if mtu <= 0 {
		mtu = 1400
	}
	runAllowFail("ip", "link", "set", "dev", name, "mtu", fmt.Sprintf("%d", mtu))
	runAllowFail("ip", "link", "set", "dev", name, "txqueuelen", "10000")
	runAllowFail("ip", "link", "set", "dev", name, "up")
	return nil
}

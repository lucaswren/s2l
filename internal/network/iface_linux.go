//go:build linux

package network

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// WaitInterface 等待网卡出现并 up，供 sing-box 创建 TUN 后使用。
func WaitInterface(name string, timeout time.Duration) error {
	if name == "" {
		return fmt.Errorf("interface name is empty")
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	deadline := time.Now().Add(timeout)
	sysPath := filepath.Join("/sys/class/net", name)

	for time.Now().Before(deadline) {
		if _, err := os.Stat(sysPath); err == nil {
			runAllowFail("ip", "link", "set", "dev", name, "up")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("interface %q not found within %s (sing-box 可能未成功创建 TUN)", name, timeout)
}

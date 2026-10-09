//go:build linux

package l2tp

import (
	"fmt"
	"os/exec"
	"strings"
)

// Reload 触发 xl2tpd 加载最新配置；若服务已 failed 则 restart。
func Reload() error {
	stateOut, _ := exec.Command("systemctl", "is-active", "xl2tpd").CombinedOutput()
	state := strings.TrimSpace(string(stateOut))

	if state == "active" {
		if out, err := exec.Command("systemctl", "reload", "xl2tpd").CombinedOutput(); err == nil {
			return nil
		} else {
			// reload 不支持时走 restart
			_ = out
		}
	}

	// failed / inactive / reload 失败：强制拉起
	_ = exec.Command("pkill", "-x", "xl2tpd").Run()
	if out, err := exec.Command("systemctl", "restart", "xl2tpd").CombinedOutput(); err != nil {
		return fmt.Errorf("restart xl2tpd: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

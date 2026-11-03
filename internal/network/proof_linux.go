//go:build linux

package network

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lucaswren/s2l/internal/debuglog"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/speedtest"
)

// ProbeForwarding injects TCP traffic through the real PPP ingress guard and
// policy route, using an isolated, temporary peer. It never changes a mapping's
// routes, firewall allowances or upstream. Authentication is tested separately.
func ProbeForwarding(ctx context.Context, m model.TunnelMapping) (result model.ProtocolHealth) {
	start := time.Now()
	result = model.ProtocolHealth{State: "unavailable", Method: "ppp-ingress-https", Stage: "test-entry"}
	cleanupFailure := func() {
		if result.State == "healthy" {
			result.State = "degraded"
		}
		if result.Error != "" {
			result.Error += "；"
		}
		result.Error += "临时测试入口清理失败，请检查诊断日志"
	}
	finish := func() model.ProtocolHealth {
		result.CheckedAt = time.Now()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}
	if err := validateMapping(m); err != nil {
		result.Error = "映射参数无效"
		return finish()
	}
	if _, err := exec.LookPath("curl"); err != nil {
		result.State = "unsupported"
		result.Error = "映射实测需要 curl"
		return finish()
	}
	source, err := proofSource(m.Subnet)
	if err != nil {
		result.State = "unsupported"
		result.Error = err.Error()
		return finish()
	}
	var token [5]byte
	if _, err = rand.Read(token[:]); err != nil {
		result.Error = "无法创建测试标识"
		return finish()
	}
	suffix := hex.EncodeToString(token[:])
	ns := fmt.Sprintf("s2l-proof-%d-%s", os.Getpid(), suffix)
	host := "pppst" + suffix
	peer := "probe0"
	command := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "ip", args...)
		cmd.WaitDelay = time.Second
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		b, e := cmd.CombinedOutput()
		if e != nil {
			message := string(b)
			if len(message) > 2048 {
				message = message[:2048]
			}
			debuglog.Default.Printf("mapping %s diagnostic command failed: %v; %s", m.ID, e, message)
		}
		return string(b), e
	}
	// Do not shadow a live peer's address, including a leftover diagnostic peer.
	routes, e := command("-j", "route", "show", "exact", source+"/32")
	if e != nil || strings.TrimSpace(routes) != "[]" {
		result.Error = "测试地址不可用"
		return finish()
	}
	if _, err = command("netns", "add", ns); err != nil {
		result.Error = "无法创建隔离测试入口"
		return finish()
	}
	defer func() {
		if err := run("ip", "netns", "del", ns); err != nil {
			debuglog.Default.Printf("mapping %s diagnostic cleanup: %v", m.ID, err)
			cleanupFailure()
		}
	}()
	if _, err = command("link", "add", host, "type", "veth", "peer", "name", peer, "netns", ns); err != nil {
		result.Error = "无法创建测试接口"
		return finish()
	}
	defer func() {
		if err := run("ip", "link", "del", host); err != nil {
			debuglog.Default.Printf("mapping %s diagnostic cleanup: %v", m.ID, err)
			cleanupFailure()
		}
	}()
	for _, args := range [][]string{
		{"addr", "add", "169.254.255.1", "peer", source + "/32", "dev", host},
		{"link", "set", host, "mtu", fmt.Sprint(m.EffectiveMTU()), "up"},
		{"netns", "exec", ns, "ip", "addr", "add", source, "peer", "169.254.255.1/32", "dev", peer},
		{"netns", "exec", ns, "ip", "link", "set", peer, "mtu", fmt.Sprint(m.EffectiveMTU()), "up"},
		{"netns", "exec", ns, "ip", "link", "set", "lo", "up"},
		{"netns", "exec", ns, "ip", "route", "add", "default", "via", "169.254.255.1", "dev", peer},
	} {
		if _, err = command(args...); err != nil {
			result.Error = "测试入口配置失败"
			return finish()
		}
	}
	result.Stage = "forwarding"
	// Numeric HTTPS endpoint avoids host DNS and proxy environment variables.
	body, err := command("netns", "exec", ns, "curl", "--disable", "--fail", "--noproxy", "*", "--silent", "--show-error", "--max-time", "8", "--connect-timeout", "4", "--max-filesize", "4096", "--proto", "=https", "https://1.1.1.1/cdn-cgi/trace")
	if err != nil {
		result.Error = "映射 HTTPS 实测失败；请结合节点检测和规则检查定位"
		return finish()
	}
	ip := speedtest.ParseExitIP(body)
	if ip == "" {
		result.Error = "映射实测未收到有效出口信息"
		return finish()
	}
	result.State = "healthy"
	result.Stage = "http"
	result.Error = ""
	result.Method = "ppp-ingress-https"
	result.ExitIP = ip
	return finish()
}

// Only reclaim namespaces bearing our exact ownership marker and a dead owner.
// Never signal a process discovered by name or touch an active diagnostic.
func CleanupProofs() error {
	data, err := output("ip", "netns", "list")
	if err != nil {
		return err
	}
	pattern := regexp.MustCompile(`^s2l-proof-([1-9][0-9]*)-([0-9a-f]{10})$`)
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		match := pattern.FindStringSubmatch(fields[0])
		if match == nil {
			continue
		}
		pid, err := strconv.Atoi(match[1])
		if err != nil || syscall.Kill(pid, 0) != syscall.ESRCH {
			continue
		}
		_ = run("ip", "link", "del", "pppst"+match[2])
		if err := run("ip", "netns", "del", fields[0]); err != nil {
			return err
		}
	}
	return nil
}

func proofSource(subnet string) (string, error) {
	_, n, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", err
	}
	ones, bits := n.Mask.Size()
	if bits != 32 || ones > 29 {
		return "", fmt.Errorf("子网没有独立测试地址；需至少 /29")
	}
	ip := n.IP.To4()
	for i := range ip {
		ip[i] |= ^n.Mask[i]
	}
	ip[3]--
	return ip.String(), nil
}

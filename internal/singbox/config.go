package singbox

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
)

const configDir = "/run/s2l"

// RuntimeDir is private to the root service, rather than a shared temporary directory.
func RuntimeDir() string { return configDir }

func ensureRuntimeDir() error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(configDir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("runtime directory %s must be a private directory (0700)", configDir)
	}
	return nil
}

// ConfigPath 返回映射对应的 sing-box 配置文件路径
func ConfigPath(tunName string) string {
	return filepath.Join(configDir, fmt.Sprintf("s2l-%s.json", tunName))
}

// TunInet4Address 为 TUN 分配点对点地址（与 L2TP Subnet 独立）
// 使用 172.19.<tableID%256>.1/30，避免与常见业务网段冲突。
func TunInet4Address(tableID int) string {
	octet := tableID % 256
	if octet < 0 {
		octet = -octet
	}
	return fmt.Sprintf("172.19.%d.1/30", octet)
}

// GenerateConfig 根据 SOCKS5 节点与映射生成 sing-box JSON 配置内容。
// Resolve intercepted DNS via the SOCKS5 upstream using DoH and TCP DNS.
// This avoids depending on SOCKS5 UDP support for DNS resolution.
func GenerateConfig(node model.SocksNode, mapping model.TunnelMapping, resolverAddress ...string) ([]byte, error) {
	if err := mapping.ValidateOptions(); err != nil {
		return nil, err
	}
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(node.Addr))
	if err != nil {
		return nil, fmt.Errorf("invalid socks addr %q: %w", node.Addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid socks port in %q", node.Addr)
	}
	if mapping.TunName == "" {
		return nil, fmt.Errorf("tun_name is required")
	}

	level := "warn"
	cfg := map[string]any{
		"log": map[string]any{
			"level":     level,
			"timestamp": true,
		},
		"dns": map[string]any{
			"servers": []map[string]any{
				{
					"tag":      "doh",
					"address":  "https://1.1.1.1/dns-query",
					"detour":   "proxy",
					"strategy": "ipv4_only",
				},
				{
					"tag":      "tcpdns",
					"address":  "tcp://8.8.8.8",
					"detour":   "proxy",
					"strategy": "ipv4_only",
				},
			},
			"final":             "doh",
			"strategy":          "ipv4_only",
			"independent_cache": true,
		},
		"inbounds": []map[string]any{
			{
				"type":           "tun",
				"tag":            "tun-in",
				"interface_name": mapping.TunName,
				"address":        []string{TunInet4Address(mapping.TableID)},
				"mtu":            mapping.EffectiveMTU(),
				"auto_route":     false,
				"strict_route":   false,
				"stack":          "system",
			},
		},
		"outbounds": []map[string]any{
			{
				"type":        "socks",
				"tag":         "proxy",
				"server":      host,
				"server_port": port,
				"version":     "5",
				"username":    node.Username,
				"password":    node.Password,
			},
			{
				"type": "block",
				"tag":  "block",
			},
		},
		"route": map[string]any{
			"auto_detect_interface": true,
			"rules": []map[string]any{
				{
					"port":   53,
					"action": "hijack-dns",
				},
				// Avoid QUIC black holes on SOCKS5 relays with unreliable UDP.
				// Keep other UDP applications on the selected upstream.
				{
					"network": "udp",
					"port":    443,
					"action":  "reject",
					"method":  "default",
					"no_drop": true,
				},
			},
			"final": "proxy",
		},
	}
	if len(resolverAddress) > 0 {
		host, portText, err := net.SplitHostPort(resolverAddress[0])
		resolverPort, portErr := strconv.Atoi(portText)
		if err != nil || host != "127.0.0.1" || portErr != nil || resolverPort < 1 || resolverPort > 65535 {
			return nil, fmt.Errorf("DNS resolver must be on IPv4 loopback")
		}
		cfg["dns"] = map[string]any{
			"servers": []map[string]any{{"tag": "local-proxy", "address": "tcp://" + resolverAddress[0], "strategy": "ipv4_only", "detour": "dns-loopback"}},
			"final":   "local-proxy", "strategy": "ipv4_only", "independent_cache": true,
		}
		// DNS-only direct dialer is pinned to the private loopback resolver.
		cfg["outbounds"] = append(cfg["outbounds"].([]map[string]any), map[string]any{"type": "direct", "tag": "dns-loopback", "override_address": "127.0.0.1", "override_port": resolverPort})
	}
	route := cfg["route"].(map[string]any)
	rules := route["rules"].([]map[string]any)
	switch mapping.EffectiveUDPPolicy() {
	case "allow":
		rules = append(rules[:1], rules[2:]...)
	case "reject":
		delete(rules[1], "port")
	}
	route["rules"] = rules

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal sing-box config: %w", err)
	}
	return data, nil
}

// WriteConfig writes credentials only inside the private runtime directory.
func WriteConfig(node model.SocksNode, mapping model.TunnelMapping, resolverAddress ...string) (string, error) {
	data, err := GenerateConfig(node, mapping, resolverAddress...)
	if err != nil {
		return "", err
	}
	path := ConfigPath(mapping.TunName)
	if err := ensureRuntimeDir(); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(configDir, ".config-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return "", fmt.Errorf("write config %s: %w", path, err)
	}
	return path, nil
}

// RemoveConfig 删除映射对应的配置文件（忽略不存在）
func RemoveConfig(tunName string) error {
	path := ConfigPath(tunName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

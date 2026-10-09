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

const configDir = "/tmp"

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
// 多数 SK5 不支持 UDP，必须劫持 DNS 并用 DoH/TCP 经代理解析，否则下游打不开网页。
func GenerateConfig(node model.SocksNode, mapping model.TunnelMapping) ([]byte, error) {
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

	cfg := map[string]any{
		"log": map[string]any{
			"level":     "info",
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
				"type":                     "tun",
				"tag":                      "tun-in",
				"interface_name":           mapping.TunName,
				"address":                  []string{TunInet4Address(mapping.TableID)},
				"mtu":                      1400,
				"auto_route":               false,
				"strict_route":             false,
				"stack":                    "system",
				"sniff":                    true,
				"sniff_override_destination": true,
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
				"type": "direct",
				"tag":  "direct",
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
					"protocol": "dns",
					"action":   "hijack-dns",
				},
			},
			"final": "proxy",
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal sing-box config: %w", err)
	}
	return data, nil
}

// WriteConfig 生成并写入 /tmp/s2l-<tun_name>.json
func WriteConfig(node model.SocksNode, mapping model.TunnelMapping) (string, error) {
	data, err := GenerateConfig(node, mapping)
	if err != nil {
		return "", err
	}
	path := ConfigPath(mapping.TunName)
	if err := os.WriteFile(path, data, 0o600); err != nil {
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

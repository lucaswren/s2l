package config

import (
	"encoding/json"
	"fmt"
	"github.com/lucaswren/s2l/internal/model"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config 运行时配置
type Config struct {
	VPNAddress    string                 `json:"vpn_address,omitempty"`
	UILanguage    string                 `json:"ui_language,omitempty"`
	PPPManagement model.ManagementAccess `json:"ppp_management"`
	Listen        string                 `json:"listen"`       // 如 :8080
	AdminUser     string                 `json:"admin_user"`   // 管理用户名；网页登录与脚本认证共用
	AdminPass     string                 `json:"admin_pass"`   // 管理密码
	SingboxBin    string                 `json:"singbox_bin"`  // sing-box 路径
	DataDir       string                 `json:"data_dir"`     // 状态持久化目录
	ChapSecrets   string                 `json:"chap_secrets"` // /etc/ppp/chap-secrets
	Xl2tpdConf    string                 `json:"xl2tpd_conf"`  // /etc/xl2tpd/xl2tpd.conf
	PPPOptions    string                 `json:"ppp_options"`  // /etc/ppp/options.xl2tpd
	AutoRestore   *bool                  `json:"auto_restore"` // 开机恢复 running 映射，默认 true
}

func Default() Config {
	t := true
	return Config{
		Listen:      "127.0.0.1:8080",
		SingboxBin:  "sing-box",
		DataDir:     "./data",
		ChapSecrets: "/etc/ppp/chap-secrets",
		Xl2tpdConf:  "/etc/xl2tpd/xl2tpd.conf",
		PPPOptions:  "/etc/ppp/options.xl2tpd",
		AutoRestore: &t,
	}
}

// Load 从指定 JSON 文件加载；未指定路径时返回默认配置。
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if strings.TrimSpace(string(data)) == "null" {
		return cfg, fmt.Errorf("config must be a JSON object")
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	def := Default()
	if cfg.Listen == "" {
		cfg.Listen = def.Listen
	}
	if cfg.SingboxBin == "" {
		cfg.SingboxBin = def.SingboxBin
	}
	if cfg.DataDir == "" {
		cfg.DataDir = def.DataDir
	}
	if cfg.ChapSecrets == "" {
		cfg.ChapSecrets = def.ChapSecrets
	}
	if cfg.Xl2tpdConf == "" {
		cfg.Xl2tpdConf = def.Xl2tpdConf
	}
	if cfg.PPPOptions == "" {
		cfg.PPPOptions = def.PPPOptions
	}
	if cfg.AutoRestore == nil {
		cfg.AutoRestore = def.AutoRestore
	}
	return cfg, nil
}

func (c Config) StatePath() string {
	return filepath.Join(c.DataDir, "state.json")
}

func (c Config) ShouldAutoRestore() bool {
	return c.AutoRestore == nil || *c.AutoRestore
}

// Validate prevents exposing the unauthenticated management API on a network
// interface. Loopback-only deployments may omit credentials for local use.
func (c Config) Validate() error {
	if !ValidVPNAddress(c.VPNAddress) {
		return fmt.Errorf("invalid vpn_address")
	}
	if c.UILanguage != "" && c.UILanguage != "zh-CN" && c.UILanguage != "zh-TW" && c.UILanguage != "en" {
		return fmt.Errorf("invalid ui_language")
	}
	if err := c.PPPManagement.Validate(); err != nil {
		return err
	}
	if c.PPPManagement.Enabled && (c.AdminUser == "" || c.AdminPass == "") {
		return fmt.Errorf("PPP management access requires admin authentication")
	}
	if c.AdminUser == "" != (c.AdminPass == "") {
		return fmt.Errorf("admin_user and admin_pass must be configured together")
	}
	if c.AdminUser != "" && len(c.AdminPass) < 12 {
		return fmt.Errorf("admin_pass must be at least 12 characters")
	}
	if strings.ContainsAny(c.AdminUser+c.AdminPass, "\r\n\x00") || strings.Contains(c.AdminUser, ":") {
		return fmt.Errorf("admin credentials contain unsupported characters")
	}
	host, portText, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", c.Listen, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("listen port must be between 1 and 65535")
	}
	if c.AdminUser == "" && host != "localhost" && !isLoopbackIP(host) {
		return fmt.Errorf("management listener %q is not loopback; configure admin_user and admin_pass", c.Listen)
	}
	return nil
}

func isLoopbackIP(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidVPNAddress accepts an optional IPv4 or DNS host without a port or scheme.
func ValidVPNAddress(address string) bool {
	if address == "" {
		return true
	}
	if ip := net.ParseIP(address); ip != nil {
		return !strings.Contains(address, ":") && ip.To4() != nil
	}
	host := strings.TrimSuffix(address, ".")
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	hasLetter := false
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			switch {
			case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z':
				hasLetter = true
			case ch >= '0' && ch <= '9', ch == '-':
			default:
				return false
			}
		}
	}
	return hasLetter
}

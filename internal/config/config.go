package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config 运行时配置
type Config struct {
	Listen      string `json:"listen"`       // 如 :8080
	AdminUser   string `json:"admin_user"`   // 管理界面 Basic Auth 用户名
	AdminPass   string `json:"admin_pass"`   // 管理界面 Basic Auth 密码
	SingboxBin  string `json:"singbox_bin"`  // sing-box 路径
	DataDir     string `json:"data_dir"`     // 状态持久化目录
	ChapSecrets string `json:"chap_secrets"` // /etc/ppp/chap-secrets
	Xl2tpdConf  string `json:"xl2tpd_conf"`  // /etc/xl2tpd/xl2tpd.conf
	PPPOptions  string `json:"ppp_options"`  // /etc/ppp/options.xl2tpd
	AutoRestore *bool  `json:"auto_restore"` // 开机恢复 running 映射，默认 true
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

// Load 从 JSON 文件加载；文件不存在则返回默认配置。
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
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

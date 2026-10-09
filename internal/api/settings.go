package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/lucaswren/s2l/internal/config"
)

func (s *Server) ConfigureSettings(path string) { s.configPath = path }

func (s *Server) settingsWrite(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, 405, "仅支持 POST")
		return false
	}
	if s.configPath == "" {
		writeError(w, 409, "未指定配置文件，请使用 -config 启动服务")
		return false
	}
	s.authMu.RLock()
	enabled := s.adminUser != "" && s.adminPass != ""
	s.authMu.RUnlock()
	if !enabled {
		writeError(w, 403, "请先在配置文件中启用管理账号认证")
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host {
			writeError(w, 403, "不允许跨站修改设置")
			return false
		}
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeError(w, 415, "请使用 application/json")
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	return true
}

func sshPorts() ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/sshd", "-T").Output()
	if err != nil {
		return nil, err
	}
	ports := []int{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "port" {
			if p, e := strconv.Atoi(fields[1]); e == nil {
				ports = append(ports, p)
			}
		}
	}
	return ports, nil
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, 405, "仅支持 GET")
		return
	}
	s.authMu.RLock()
	user := s.adminUser
	enabled := user != "" && s.adminPass != ""
	s.authMu.RUnlock()
	ports, err := sshPorts()
	if ports == nil {
		ports = []int{}
	}
	_, helperErr := os.Stat("/opt/s2l/manage_ssh.py")
	webURL, webPort, webEnabled := s.webPortSettings()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"public_url": webURL, "web_port": webPort, "web_port_enabled": enabled && webEnabled, "admin_user": user, "account_enabled": enabled && s.configPath != "", "ssh_ports": ports, "ssh_password_enabled": enabled && s.configPath != "" && sshPasswordAvailable(), "ipsec": readIPsecStatus(), "ssh_enabled": enabled && s.configPath != "" && runtime.GOOS == "linux" && err == nil && helperErr == nil})
}

func (s *Server) webPortSettings() (string, int, bool) {
	data, err := os.ReadFile(s.configPath)
	if err != nil {
		return "", 0, false
	}
	var fields struct {
		PublicURL string `json:"public_url"`
	}
	if json.Unmarshal(data, &fields) != nil {
		return "", 0, false
	}
	u, err := url.Parse(fields.PublicURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return "", 0, false
	}
	port := 443
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return "", 0, false
		}
	}
	_, helperErr := os.Stat("/opt/s2l/manage_config.py")
	site, siteErr := os.ReadFile("/etc/nginx/conf.d/s2l.conf")
	available := runtime.GOOS == "linux" && filepath.Clean(s.configPath) == "/opt/s2l/config.json" && helperErr == nil && siteErr == nil && strings.HasPrefix(string(site), "# Managed by s2l HTTPS\n")
	return fields.PublicURL, port, available
}

func (s *Server) handleWebPort(w http.ResponseWriter, r *http.Request) {
	if !s.settingsWrite(w, r) {
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	var body struct {
		Port int `json:"port"`
	}
	if decodeJSON(r, &body) != nil || body.Port < 1 || body.Port > 65535 || body.Port == 80 {
		writeError(w, 400, "HTTPS 端口需为 1–65535，不能为 80")
		return
	}
	if _, _, enabled := s.webPortSettings(); !enabled {
		writeError(w, 409, "仅支持 s2l 管理的 Nginx HTTPS 服务，请升级安装文件")
		return
	}
	cmd := exec.Command("python3", "/opt/s2l/manage_config.py", "listen_port")
	cmd.Stdin = strings.NewReader(strconv.Itoa(body.Port))
	out, err := cmd.CombinedOutput()
	if err != nil {
		writeError(w, 500, fmt.Sprintf("HTTPS 端口修改失败：%s", strings.TrimSpace(string(out))))
		return
	}
	address, port, _ := s.webPortSettings()
	writeJSON(w, 200, map[string]any{"public_url": address, "port": port, "message": "HTTPS 端口已更新，请使用新地址登录"})
}

func (s *Server) checkCurrent(password string) bool {
	s.authMu.RLock()
	defer s.authMu.RUnlock()
	return subtle.ConstantTimeCompare([]byte(password), []byte(s.adminPass)) == 1
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	if !s.settingsWrite(w, r) {
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`).MatchString(body.Username) {
		writeError(w, 400, "用户名需为 1–64 位字母、数字、点、下划线或连字符")
		return
	}
	s.authMu.RLock()
	password := s.adminPass
	s.authMu.RUnlock()
	if body.Password != "" {
		if !regexp.MustCompile(`^[A-Za-z0-9_.@+-]{12,128}$`).MatchString(body.Password) {
			writeError(w, 400, "密码需为 12–128 位字母、数字或 _ . @ + -")
			return
		}
		password = body.Password
	}
	data, err := os.ReadFile(s.configPath)
	if err != nil {
		writeError(w, 500, "无法读取配置文件")
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		writeError(w, 500, "配置文件格式错误")
		return
	}
	fields["admin_user"], _ = json.Marshal(body.Username)
	fields["admin_pass"], _ = json.Marshal(password)
	data, err = json.MarshalIndent(fields, "", "  ")
	if err != nil {
		writeError(w, 500, "无法生成配置文件")
		return
	}
	var cfg config.Config
	if json.Unmarshal(data, &cfg) != nil || cfg.Validate() != nil {
		writeError(w, 400, "修改后的配置无效")
		return
	}
	if err = writeSettings(s.configPath, append(data, '\n')); err != nil {
		writeError(w, 500, "保存配置失败，管理账号未改变")
		return
	}
	s.authMu.Lock()
	s.adminUser, s.adminPass = body.Username, password
	s.authMu.Unlock()
	writeJSON(w, 200, map[string]string{"message": "管理账号已更新，新凭据立即生效"})
}

func writeSettings(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".s2l-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func (s *Server) handleSSH(w http.ResponseWriter, r *http.Request) {
	if !s.settingsWrite(w, r) {
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	var body struct {
		Port int `json:"port"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	if body.Port < 1 || body.Port > 65535 {
		writeError(w, 400, "端口需为 1–65535")
		return
	}
	if runtime.GOOS != "linux" {
		writeError(w, 409, "仅支持 Linux OpenSSH")
		return
	}
	cmd := exec.Command("python3", "/opt/s2l/manage_ssh.py")
	cmd.Stdin = strings.NewReader(strconv.Itoa(body.Port))
	out, err := cmd.CombinedOutput()
	if err != nil {
		writeError(w, 500, fmt.Sprintf("SSH 端口修改失败：%s", strings.TrimSpace(string(out))))
		return
	}
	writeJSON(w, 200, map[string]any{"port": body.Port, "message": strings.TrimSpace(string(out))})
}

// readIPsecStatus never returns the pre-shared key.
func readIPsecStatus() map[string]any {
	result := map[string]any{"available": false, "enabled": false, "active": false, "psk_configured": false, "protected": false}
	if runtime.GOOS != "linux" {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "python3", "/opt/s2l/manage_ipsec.py", "status").Output()
	if err != nil {
		return result
	}
	if json.Unmarshal(out, &result) != nil {
		return map[string]any{"available": false}
	}
	return result
}
func (s *Server) handleIPsec(w http.ResponseWriter, r *http.Request) {
	if !s.settingsWrite(w, r) {
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	var body struct {
		Enabled         *bool  `json:"enabled"`
		PSK             string `json:"psk"`
		CurrentPassword string `json:"current_password"`
	}
	if decodeJSON(r, &body) != nil || body.Enabled == nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	if !s.checkCurrent(body.CurrentPassword) {
		writeError(w, 403, "当前管理密码不正确")
		return
	}
	if body.PSK != "" && !regexp.MustCompile(`^[A-Za-z0-9_.@+-]{12,128}$`).MatchString(body.PSK) {
		writeError(w, 400, "预共享密钥需为 12–128 位字母、数字或 _ . @ + -")
		return
	}
	if runtime.GOOS != "linux" {
		writeError(w, 409, "IPsec 仅支持 Linux 服务器")
		return
	}
	payload, _ := json.Marshal(map[string]any{"enabled": *body.Enabled, "psk": body.PSK})
	cmd := exec.Command("python3", "/opt/s2l/manage_ipsec.py", "apply")
	cmd.Stdin = strings.NewReader(string(payload))
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = "请检查 IPsec 管理脚本及系统服务"
		}
		writeError(w, 500, message)
		return
	}
	var result map[string]any
	if json.Unmarshal(out, &result) != nil {
		writeError(w, 500, "IPsec 服务返回异常，请刷新设置查看状态")
		return
	}
	writeJSON(w, 200, result)
}

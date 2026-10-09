package api

import (
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// HTTPS termination is trusted only when the immediate peer is loopback.
func secureSettingsRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	peer := net.ParseIP(host)
	return err == nil && peer != nil && peer.IsLoopback() && r.Header.Get("X-Forwarded-Proto") == "https"
}

func sshPasswordAvailable() bool {
	_, err := exec.LookPath("chpasswd")
	return runtime.GOOS == "linux" && os.Geteuid() == 0 && err == nil
}

func (s *Server) handleSSHPassword(w http.ResponseWriter, r *http.Request) {
	if !s.settingsWrite(w, r) {
		return
	}
	if !secureSettingsRequest(r) {
		writeError(w, 403, "SSH 登录密码只能通过 HTTPS 修改")
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	var body struct {
		Password        string `json:"password"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	if body.Password != body.ConfirmPassword {
		writeError(w, 400, "两次输入的新 SSH 密码不一致")
		return
	}
	if len(body.Password) < 12 || len(body.Password) > 128 {
		writeError(w, 400, "SSH 密码需为 12–128 位可见 ASCII 字符")
		return
	}
	for _, c := range body.Password {
		if c < 33 || c > 126 {
			writeError(w, 400, "SSH 密码只能包含可见 ASCII 字符，不支持空格或控制字符")
			return
		}
	}
	if !sshPasswordAvailable() {
		writeError(w, 409, "当前环境不支持修改 Linux 用户密码")
		return
	}
	// Limit this endpoint to the existing root account; never accept a shell command or user name.
	output, err := exec.Command("passwd", "--status", "root").Output()
	fields := strings.Fields(string(output))
	if err != nil || len(fields) < 2 || fields[0] != "root" {
		writeError(w, 500, "无法读取 root 账号状态")
		return
	}
	if fields[1] == "L" {
		writeError(w, 409, "root 账号已锁定，请通过 SSH 或服务器控制台处理")
		return
	}
	cmd := exec.Command("chpasswd")
	cmd.Stdin = strings.NewReader("root:" + body.Password + "\n")
	// Passwords never appear in command arguments, application logs, or responses.
	if cmd.Run() != nil {
		writeError(w, 500, "SSH 密码更新失败，请检查系统账号和密码策略")
		return
	}
	user, _, _ := r.BasicAuth()
	log.Printf("security: root password updated by management user %q", user)
	writeJSON(w, 200, map[string]string{"message": "root 登录密码已更新，请保留当前连接并在新终端验证登录"})
}

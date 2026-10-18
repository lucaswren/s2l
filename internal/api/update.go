package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/lucaswren/s2l/internal/buildinfo"
)

type updateRequest struct {
	Action string `json:"action"`
	Tag    string `json:"tag,omitempty"`
	Commit string `json:"commit,omitempty"`
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	body := updateRequest{Action: "status"}
	if r.Method == http.MethodPost {
		if !s.settingsWrite(w, r) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if decodeJSON(r, &body) != nil || (body.Action != "check" && body.Action != "start") {
			writeError(w, 400, "invalid update action")
			return
		}
	}
	s.authMu.RLock()
	authenticated := s.adminUser != "" && s.adminPass != ""
	s.authMu.RUnlock()
	_, helperErr := os.Stat("/opt/s2l/manage_update.py")
	available := runtime.GOOS == "linux" && os.Geteuid() == 0 && filepath.Clean(s.configPath) == filepath.Clean("/opt/s2l/config.json") && authenticated && helperErr == nil
	if !available {
		if r.Method == http.MethodPost {
			writeError(w, 409, "网页更新需要默认 Linux/systemd 安装、管理认证及新版更新组件")
			return
		}
		writeJSON(w, 200, map[string]any{"available": false, "current": buildinfo.Current(), "message": "网页更新需默认 Linux/systemd 安装及新版更新组件"})
		return
	}
	if body.Action == "start" {
		s.updateGate.Lock()
		defer s.updateGate.Unlock()
	}
	select {
	case s.updateTasks <- struct{}{}:
		defer func() { <-s.updateTasks }()
	default:
		writeError(w, 429, "更新操作正在处理中，请稍后重试")
		return
	}
	base := r.Context()
	if body.Action == "start" {
		base = context.WithoutCancel(base)
	}
	ctx, cancel := context.WithTimeout(base, 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "/opt/s2l/manage_update.py", body.Action)
	configureProbeCommand(cmd)
	cmd.WaitDelay = time.Second
	payload, _ := json.Marshal(body)
	cmd.Stdin = bytes.NewReader(payload)
	output := &boundedOutput{}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	var result map[string]any
	output.mu.Lock()
	decodeErr := json.Unmarshal(output.data, &result)
	output.mu.Unlock()
	if decodeErr != nil || result == nil {
		writeError(w, 500, "无法读取更新状态，请检查系统更新组件")
		return
	}
	if err != nil {
		message, _ := result["error"].(string)
		if message == "" {
			message = "更新操作失败"
		}
		writeError(w, 409, message)
		return
	}
	result["available"] = true
	result["current"] = buildinfo.Current()
	code := 200
	if body.Action == "start" {
		code = http.StatusAccepted
	}
	writeJSON(w, code, result)
}

// New writes must not race the upgrade backup or post-restart verification.
// Debug is memory-only and remains available while diagnosing an update.
func (s *Server) withUpdateProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodPost && r.Method != http.MethodDelete) || r.URL.Path == "/api/settings/update" || r.URL.Path == "/api/debug" || filepath.Clean(s.configPath) != filepath.Clean("/opt/s2l/config.json") {
			next.ServeHTTP(w, r)
			return
		}
		s.updateGate.RLock()
		defer s.updateGate.RUnlock()
		pending, err := updatePending(s.updateStatePath)
		if err != nil {
			writeError(w, 503, "无法确认更新状态，请检查软件更新任务")
			return
		}
		if pending {
			writeError(w, 409, "软件正在更新，请完成后再修改配置或操作映射")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func updatePending(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 16384 {
		return false, fmt.Errorf("invalid update state file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil {
		return false, err
	}
	if len(data) > 16384 {
		return false, fmt.Errorf("update state too large")
	}
	var job struct {
		Status string `json:"status"`
	}
	if err = json.Unmarshal(data, &job); err != nil {
		return false, err
	}
	switch job.Status {
	case "queued", "running":
		return true, nil
	case "succeeded", "failed", "interrupted":
		return false, nil
	default:
		return false, fmt.Errorf("invalid update state")
	}
}

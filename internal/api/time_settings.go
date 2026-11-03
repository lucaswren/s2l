package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"
)

func (s *Server) handleTimeSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	if runtime.GOOS != "linux" {
		writeError(w, 501, "系统时间设置仅支持 Linux / systemd")
		return
	}
	if _, err := os.Stat("/opt/s2l/manage_time.py"); err != nil {
		writeError(w, 501, "缺少时间管理组件，请更新安装文件")
		return
	}
	action := "status"
	var input []byte
	if r.Method == http.MethodPost {
		if !s.settingsWrite(w, r) {
			return
		}
		var body json.RawMessage
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		defer r.Body.Close()
		if decodeJSON(r, &body) != nil {
			writeError(w, 400, "invalid json")
			return
		}
		input = body
		action = "apply"
	}
	select {
	case s.timeSettings <- struct{}{}:
		defer func() { <-s.timeSettings }()
	default:
		writeError(w, 429, "时间服务正在处理中，请稍后重试")
		return
	}
	// A disconnected client must not interrupt a partially applied system change.
	// The helper serializes across processes and rolls back any failed mutation.
	base := r.Context()
	if action == "apply" {
		base = context.WithoutCancel(base)
	}
	ctx, cancel := context.WithTimeout(base, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "/opt/s2l/manage_time.py", action)
	configureProbeCommand(cmd)
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(input)
	output := &boundedOutput{}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	var result map[string]any
	output.mu.Lock()
	decodeErr := json.Unmarshal(output.data, &result)
	output.mu.Unlock()
	if decodeErr != nil || result == nil {
		writeError(w, 500, "读取系统时间服务失败，请检查 systemd 和权限")
		return
	}
	if err != nil {
		message, _ := result["error"].(string)
		if message == "" {
			message = "系统时间服务操作失败"
		}
		writeError(w, 409, message)
		return
	}
	result["server_time"] = time.Now().UTC()
	writeJSON(w, 200, result)
}

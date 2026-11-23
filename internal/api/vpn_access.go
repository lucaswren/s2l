package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/lucaswren/s2l/internal/config"
)

func (s *Server) handleVPNAddress(w http.ResponseWriter, r *http.Request) {
	if !s.settingsWrite(w, r) {
		return
	}
	var body struct {
		Address *string `json:"address"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if decodeJSON(r, &body) != nil || body.Address == nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	address := strings.TrimSpace(*body.Address)
	if !config.ValidVPNAddress(address) {
		writeError(w, 400, "请输入 IPv4 地址或域名，不含协议或端口")
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	unlock, err := lockSettingsFile(r.Context(), s.configPath)
	if err != nil {
		writeError(w, 503, "无法锁定配置文件，请重试")
		return
	}
	defer unlock()
	raw, err := os.ReadFile(s.configPath)
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &fields) != nil || fields == nil {
		writeError(w, 409, "无法读取受管配置文件")
		return
	}
	fields["vpn_address"], _ = json.Marshal(address)
	data, err := json.MarshalIndent(fields, "", "  ")
	if err != nil || writeSettings(s.configPath, data) != nil {
		writeError(w, 500, "无法写入配置文件")
		return
	}
	writeJSON(w, 200, map[string]string{"vpn_address": address})
}

type dialEvent struct {
	At    time.Time `json:"at"`
	Stage string    `json:"stage"`
}

func dialEventFromJournal(line string) (dialEvent, bool) {
	var record struct {
		Message string `json:"MESSAGE"`
		Source  string `json:"SYSLOG_IDENTIFIER"`
		Comm    string `json:"_COMM"`
		At      string `json:"__REALTIME_TIMESTAMP"`
	}
	if json.Unmarshal([]byte(line), &record) != nil {
		return dialEvent{}, false
	}
	at, err := strconv.ParseInt(record.At, 10, 64)
	if err != nil || at <= 0 {
		return dialEvent{}, false
	}
	message := strings.ToLower(record.Message)
	var stage string
	source := record.Source
	if record.Comm == "pppd" || record.Comm == "xl2tpd" || record.Comm == "charon" {
		source = record.Comm
	}
	switch source {
	case "xl2tpd":
		switch {
		case strings.Contains(message, "call established"):
			stage = "l2tp-call"
		case strings.Contains(message, "connection established"):
			stage = "l2tp-control"
		}
	case "pppd":
		switch {
		case strings.Contains(message, "failed chap authentication"), strings.Contains(message, "no chap secret found"), strings.Contains(message, "authentication failed"):
			stage = "ppp-auth-failed"
		case strings.Contains(message, "authentication succeeded"):
			stage = "ppp-auth-ok"
		case strings.Contains(message, "remote ip address"):
			stage = "ppp-address"
		case strings.Contains(message, "connection terminated"):
			stage = "ppp-closed"
		}
	case "charon", "ipsec", "starter":
		switch {
		case strings.Contains(message, "received packet:") && (strings.Contains(message, "[500] (") || strings.Contains(message, "[4500] (")):
			stage = "ike-udp"
		case strings.Contains(message, "child_sa") && strings.Contains(message, "established"):
			stage = "ipsec-sa"
		case strings.Contains(message, "ike_sa") && strings.Contains(message, "established"):
			stage = "ike-sa"
		case strings.Contains(message, "authentication failed"), strings.Contains(message, "no shared key found"),
			strings.Contains(message, "no proposal chosen"), strings.Contains(message, "[enc] could not decrypt payloads"):
			stage = "ike-failed"
		}
	}
	return dialEvent{At: time.UnixMicro(at).UTC(), Stage: stage}, stage != ""
}

// A listening socket is host evidence only; it cannot prove public reachability.
func dialListeners(ctx context.Context) []int {
	data, err := exec.CommandContext(ctx, "ss", "-H", "-lnu", "( sport = :1701 or sport = :500 or sport = :4500 )").Output()
	if err != nil {
		return nil
	}
	found := map[int]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		port := fields[3]
		if i := strings.LastIndexByte(port, ':'); i >= 0 {
			if n, err := strconv.Atoi(port[i+1:]); err == nil {
				found[n] = true
			}
		}
	}
	ports := []int{}
	for _, port := range []int{500, 1701, 4500} {
		if found[port] {
			ports = append(ports, port)
		}
	}
	return ports
}

func (s *Server) handleDialTimeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	if runtime.GOOS != "linux" {
		writeJSON(w, 200, map[string]any{"available": false, "events": []dialEvent{}})
		return
	}
	select {
	case s.networkTests <- struct{}{}:
		defer func() { <-s.networkTests }()
	default:
		writeError(w, 429, "检测忙，请稍后重试")
		return
	}
	listenerCtx, stopListeners := context.WithTimeout(r.Context(), time.Second)
	listeners := dialListeners(listenerCtx)
	stopListeners()
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "journalctl", "--quiet", "--no-pager", "-b", "--since=-30min",
		"-u", "xl2tpd.service", "-u", "s2l-ipsec.service", "-n", "300", "-o", "json",
		"--output-fields=MESSAGE,SYSLOG_IDENTIFIER,_COMM,__REALTIME_TIMESTAMP")
	pipe, err := cmd.StdoutPipe()
	events := []dialEvent{}
	available := false
	if err == nil && cmd.Start() == nil {
		const maxBytes = 1 << 20
		data, readErr := io.ReadAll(io.LimitReader(pipe, maxBytes+1))
		if len(data) > maxBytes || readErr != nil {
			cancel()
		}
		if cmd.Wait() == nil && readErr == nil && len(data) <= maxBytes {
			available = true
			for _, line := range strings.Split(string(data), "\n") {
				if event, ok := dialEventFromJournal(line); ok {
					events = append(events, event)
				}
			}
			if len(events) > 60 {
				events = events[len(events)-60:]
			}
		}
	}
	writeJSON(w, 200, map[string]any{"available": available, "events": events, "udp_listeners": listeners})
}

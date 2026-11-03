package api

import (
	"encoding/json"
	"net/http"
	"os"
)

// Language is a persisted administrator preference, not a credential change.
func (s *Server) handleLanguage(w http.ResponseWriter, r *http.Request) {
	if !s.settingsWrite(w, r) {
		return
	}
	var body struct {
		Language string `json:"language"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if decodeJSON(r, &body) != nil || (body.Language != "en" && body.Language != "zh-CN" && body.Language != "zh-TW") {
		writeError(w, 400, "invalid language")
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	data, err := os.ReadFile(s.configPath)
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(data, &fields) != nil || fields == nil {
		writeError(w, 500, "无法读取配置文件")
		return
	}
	fields["ui_language"], _ = json.Marshal(body.Language)
	data, err = json.MarshalIndent(fields, "", "  ")
	if err != nil || writeSettings(s.configPath, append(data, '\n')) != nil {
		writeError(w, 500, "语言设置保存失败，请重试")
		return
	}
	s.authMu.Lock()
	s.uiLanguage = body.Language
	s.authMu.Unlock()
	writeJSON(w, 200, map[string]string{"language": body.Language})
}

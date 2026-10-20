package api

import (
	"net/http"
)

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	if s.svc == nil {
		writeError(w, 503, "运行服务不可用")
		return
	}
	select {
	case s.networkTests <- struct{}{}:
		defer func() { <-s.networkTests }()
	default:
		writeError(w, 429, "会话操作忙，请稍后重试")
		return
	}
	var b struct {
		Interface string `json:"interface"`
		Peer      string `json:"peer"`
		Index     int    `json:"index"`
	}
	if decodeJSON(r, &b) != nil {
		writeError(w, 400, "参数无效")
		return
	}
	if e := s.svc.DisconnectSession(r.Context(), b.Interface, b.Peer, b.Index); e != nil {
		writeError(w, 409, e.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

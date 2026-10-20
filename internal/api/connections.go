package api

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request, id string) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if s.svc == nil {
		writeError(w, 503, "运行服务不可用")
		return
	}
	if r.Method == http.MethodPost {
		var b struct {
			ID string `json:"id"`
		}
		if decodeJSON(r, &b) != nil || b.ID == "" || len(b.ID) > 128 {
			writeError(w, 400, "连接 ID 无效")
			return
		}
		if e := s.svc.CloseConnection(ctx, id, b.ID); e != nil {
			writeError(w, 409, e.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	rows, e := s.svc.Connections(ctx, id)
	if e != nil {
		writeError(w, 503, e.Error())
		return
	}
	writeJSON(w, 200, rows)
}

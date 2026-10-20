package api

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) handleMappingDiagnostic(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	if s.svc == nil {
		writeError(w, 503, "映射服务不可用")
		return
	}
	select {
	case s.networkTests <- struct{}{}:
		defer func() { <-s.networkTests }()
	default:
		writeError(w, 429, "检测忙，请稍后重试")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result, err := s.svc.ProbeMapping(ctx, id)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, result)
}

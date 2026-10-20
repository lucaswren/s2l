package api

import "net/http"

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	id := r.URL.Query().Get("mapping_id")
	if _, ok := s.store.GetMapping(id); !ok {
		writeError(w, 404, "映射不存在")
		return
	}
	if s.svc == nil {
		writeError(w, 503, "监控服务不可用")
		return
	}
	buckets, warning := s.svc.History(id)
	writeJSON(w, 200, map[string]any{"buckets": buckets, "warning": warning, "bucket_seconds": 300, "retention_hours": 24})
}

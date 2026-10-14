package api

import (
	"github.com/lucaswren/s2l/internal/diagnostics"
	"net/http"
)

func (s *Server) handleMappingDiagnose(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "mapping service unavailable")
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if _, err := diagnostics.ParseTarget(body.URL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	select {
	case s.diagnosticSlots <- struct{}{}:
		defer func() { <-s.diagnosticSlots }()
	default:
		writeError(w, http.StatusTooManyRequests, "diagnostics is busy; retry later")
		return
	}
	result, err := s.svc.Diagnose(r.Context(), id, body.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

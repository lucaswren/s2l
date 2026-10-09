package api

import (
	"net/http"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	st := s.svc.Status()
	mappings := redactMappings(st.Mappings)
	if mappings == nil {
		mappings = []model.TunnelMapping{}
	}
	traffic := st.TunTraffic
	if traffic == nil {
		traffic = []model.TunTraffic{}
	}
	pids := st.SingboxPIDs
	if pids == nil {
		pids = map[string]int{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uptime_sec":   int64(st.Uptime / time.Second),
		"mappings":     mappings,
		"tun_traffic":  traffic,
		"singbox_pids": pids,
	})
}

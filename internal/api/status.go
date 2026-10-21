package api

import (
	"net/http"
	"time"

	"github.com/lucaswren/s2l/internal/buildinfo"
	"github.com/lucaswren/s2l/internal/model"
)

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "mapping service unavailable")
		return
	}
	st := s.svc.CachedStatus()
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
		"build":                  buildinfo.Current(),
		"host_stats":             st.HostStats,
		"collected_at":           st.CollectedAt,
		"sessions":               st.Sessions,
		"sessions_error":         st.SessionsError,
		"sessions_checked_at":    st.SessionsCheckedAt,
		"startup_complete":       s.svc.StartupComplete(),
		"configuration_recovery": s.store.RecoveryNotice(),
		"upstream_health":        st.UpstreamHealth,
		"uptime_sec":             int64(st.Uptime / time.Second),
		"mappings":               mappings,
		"tun_traffic":            traffic,
		"singbox_pids":           pids,
	})
}

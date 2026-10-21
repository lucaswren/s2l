package api

import (
	"errors"
	"fmt"
	"github.com/lucaswren/s2l/internal/operations"
	"net/http"
	"regexp"
	"strings"
	"time"
)

func (s *Server) handleOperations(w http.ResponseWriter, r *http.Request) {
	if s.svc == nil {
		writeError(w, 503, "运行服务不可用")
		return
	}
	if r.Method == http.MethodPost {
		var c operations.Settings
		if decodeJSON(r, &c) != nil {
			writeError(w, 400, "通知参数无效")
			return
		}
		if err := s.svc.Operations().Configure(c); err != nil {
			status := 400
			if errors.Is(err, operations.ErrConflict) {
				status = 409
			}
			writeError(w, status, err.Error())
			return
		}
	} else if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	writeJSON(w, 200, s.svc.Operations().View())
}
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	if s.svc == nil {
		writeError(w, 503, "运行服务不可用")
		return
	}
	st := s.svc.CachedStatus()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintln(w, "# TYPE s2l_snapshot_timestamp_seconds gauge")
	fmt.Fprintf(w, "s2l_snapshot_timestamp_seconds %d\n", st.CollectedAt.Unix())
	for _, m := range st.Mappings {
		h := st.UpstreamHealth[m.ID]
		healthy := 0
		if m.Status == "running" && h.State == "healthy" && time.Since(h.CheckedAt) < 3*time.Minute {
			healthy = 1
		}
		fmt.Fprintf(w, "s2l_mapping_healthy{mapping=%q} %d\n", m.ID, healthy)
		fmt.Fprintf(w, "s2l_mapping_repairs_total{mapping=%q} %d\n", m.ID, h.RepairSuccesses)
		for _, t := range st.TunTraffic {
			if t.TunName == m.TunName && t.Error == "" {
				fmt.Fprintf(w, "s2l_mapping_rx_bytes_total{mapping=%q} %d\ns2l_mapping_tx_bytes_total{mapping=%q} %d\n", m.ID, t.RxBytes, m.ID, t.TxBytes)
			}
		}
	}
}

type auditWriter struct {
	http.ResponseWriter
	status int
}

func (w *auditWriter) WriteHeader(n int) {
	if w.status == 0 {
		w.status = n
		w.ResponseWriter.WriteHeader(n)
	}
}
func (w *auditWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}
func (s *Server) withAudit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.svc == nil || !strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/api/auth/") || (r.Method != http.MethodPost && r.Method != http.MethodDelete) || strings.HasSuffix(r.URL.Path, "/network-test") || strings.HasSuffix(r.URL.Path, "/speedtest") {
			next.ServeHTTP(w, r)
			return
		}
		out := &auditWriter{ResponseWriter: w}
		next.ServeHTTP(out, r)
		code := out.status
		if code == 0 {
			code = 200
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		resource := "api"
		if len(parts) > 1 {
			resource = parts[1]
			if len(parts) > 2 && regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(parts[2]) {
				resource += "/" + parts[2]
			}
		}
		s.svc.Operations().Audit("operation", resource, fmt.Sprintf("%s HTTP %d", r.Method, code))
		_ = s.svc.Operations().Flush()
	})
}

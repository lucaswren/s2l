package api

import (
	"crypto/subtle"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/service"
	"github.com/lucaswren/s2l/internal/store"
)

// Server HTTP API + 静态前端
type Server struct {
	applyManagement func(model.ManagementAccess) error
	store           *store.Store
	svc             *service.MappingService
	webFS           fs.FS // web/dist 内容；可为 nil（仅 API）
	settingsMu      sync.Mutex
	mutationMu      sync.Mutex
	authMu          sync.RWMutex
	configPath      string
	adminUser       string
	adminPass       string
	debugExports    chan struct{}
	networkTests    chan struct{}
	timeSettings    chan struct{}
	updateTasks     chan struct{}
	updateGate      sync.RWMutex
	updateStatePath string
}

func NewServer(st *store.Store, svc *service.MappingService, webFS fs.FS, credentials ...string) *Server {
	s := &Server{store: st, svc: svc, webFS: webFS, debugExports: make(chan struct{}, 2), networkTests: make(chan struct{}, 2), timeSettings: make(chan struct{}, 1), updateTasks: make(chan struct{}, 1), updateStatePath: "/var/lib/s2l/web-update/job.json"}
	if svc != nil {
		s.applyManagement = svc.SetManagementAccess
	}
	if len(credentials) >= 2 {
		s.adminUser, s.adminPass = credentials[0], credentials[1]
	}
	return s
}

// Handler 返回根 http.Handler
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/backups", s.handleBackups)
	mux.HandleFunc("/api/backups/preview", s.handleBackupRestore)
	mux.HandleFunc("/api/backups/restore", s.handleBackupRestore)
	mux.HandleFunc("/api/debug", s.handleDebug)
	mux.HandleFunc("/api/debug/analysis", s.handleDebugAnalysis)
	mux.HandleFunc("/api/debug/export", s.handleDebugExport)

	mux.HandleFunc("/api/nodes", s.handleNodes)
	mux.HandleFunc("/api/nodes/import", s.handleImportNodes)
	mux.HandleFunc("/api/nodes/", s.handleNodeByID)
	mux.HandleFunc("/api/mappings", s.handleMappings)
	mux.HandleFunc("/api/mappings/import", s.handleImportMappings)
	mux.HandleFunc("/api/mappings/", s.handleMappingByID)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/operations", s.handleOperations)
	mux.HandleFunc("/api/sessions/disconnect", s.handleSessions)
	mux.HandleFunc("/api/metrics", s.handleMetrics)
	mux.HandleFunc("/api/settings/ppp-management", s.handlePPPManagement)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/settings/time", s.handleTimeSettings)
	mux.HandleFunc("/api/settings/update", s.handleUpdate)
	mux.HandleFunc("/api/settings/account", s.handleAccount)
	mux.HandleFunc("/api/settings/ssh", s.handleSSH)
	mux.HandleFunc("/api/settings/web-port", s.handleWebPort)
	mux.HandleFunc("/api/settings/ssh-password", s.handleSSHPassword)
	mux.HandleFunc("/api/settings/ipsec", s.handleIPsec)

	if s.webFS != nil {
		fileServer := http.FileServer(http.FS(s.webFS))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			// Revalidate the entry document after upgrades; missing bundles must
			// return 404 rather than HTML with a misleading successful response.
			w.Header().Set("Cache-Control", "no-cache")
			// SPA fallback：文件不存在时回退 index.html
			path := strings.TrimPrefix(r.URL.Path, "/")
			if path == "" {
				path = "index.html"
			}
			if f, err := s.webFS.Open(path); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/assets/") || strings.Contains(path, ".") {
				http.NotFound(w, r)
				return
			}
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
		})
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{
				"name":    "S2L",
				"message": "S2L management API; build and embed web/dist to enable the web interface",
			})
		})
	}

	return s.withAuth(s.withUpdateProtection(s.withAudit(s.withMutations(mux))))
}

// Keep validation and writes atomic across API requests. State edits also share
// the lifecycle lock so a node/mapping cannot change while Start reads it.
func (s *Server) withMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimRight(r.URL.Path, "/")
		resource := r.URL.Path == "/api/nodes" || strings.HasPrefix(r.URL.Path, "/api/nodes/") ||
			r.URL.Path == "/api/mappings" || strings.HasPrefix(r.URL.Path, "/api/mappings/")
		if !resource || (r.Method != http.MethodPost && r.Method != http.MethodDelete) || (strings.HasSuffix(path, "/speedtest") || strings.HasSuffix(path, "/network-test") || strings.HasSuffix(path, "/connections")) {
			next.ServeHTTP(w, r)
			return
		}
		lifecycle := strings.HasPrefix(r.URL.Path, "/api/mappings/") &&
			(r.Method == http.MethodDelete || strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/action"))
		if lifecycle {
			next.ServeHTTP(w, r)
			return
		}
		s.mutationMu.Lock()
		defer s.mutationMu.Unlock()
		if s.svc != nil && !lifecycle {
			s.svc.SynchronizeState(func() { next.ServeHTTP(w, r) })
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withAuth(next http.Handler) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				if origin := r.Header.Get("Origin"); origin != "" {
					u, err := url.Parse(origin)
					if err != nil || u.User != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
						writeError(w, http.StatusForbidden, "cross-site API changes are not allowed")
						return
					}
				}
				if r.Method != http.MethodDelete {
					mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
					if err != nil || mediaType != "application/json" {
						writeError(w, http.StatusUnsupportedMediaType, "application/json is required")
						return
					}
				}
			}
		}
		s.authMu.RLock()
		adminUser, adminPass := s.adminUser, s.adminPass
		s.authMu.RUnlock()
		if adminUser == "" || adminPass == "" {
			next.ServeHTTP(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(adminUser)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(adminPass)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="S2L", charset="UTF-8"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

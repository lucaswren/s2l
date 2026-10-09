package api

import (
	"crypto/subtle"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/lucaswren/s2l/internal/service"
	"github.com/lucaswren/s2l/internal/store"
)

// Server HTTP API + 静态前端
type Server struct {
	store      *store.Store
	svc        *service.MappingService
	webFS      fs.FS // web/dist 内容；可为 nil（仅 API）
	settingsMu sync.Mutex
	authMu     sync.RWMutex
	configPath string
	adminUser  string
	adminPass  string
}

func NewServer(st *store.Store, svc *service.MappingService, webFS fs.FS, credentials ...string) *Server {
	s := &Server{store: st, svc: svc, webFS: webFS}
	if len(credentials) >= 2 {
		s.adminUser, s.adminPass = credentials[0], credentials[1]
	}
	return s
}

// Handler 返回根 http.Handler
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/nodes", s.handleNodes)
	mux.HandleFunc("/api/nodes/import", s.handleImportNodes)
	mux.HandleFunc("/api/nodes/", s.handleNodeByID)
	mux.HandleFunc("/api/mappings", s.handleMappings)
	mux.HandleFunc("/api/mappings/import", s.handleImportMappings)
	mux.HandleFunc("/api/mappings/", s.handleMappingByID)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/settings/account", s.handleAccount)
	mux.HandleFunc("/api/settings/ssh", s.handleSSH)
	mux.HandleFunc("/api/settings/ssh-password", s.handleSSHPassword)
	mux.HandleFunc("/api/settings/ipsec", s.handleIPsec)

	if s.webFS != nil {
		fileServer := http.FileServer(http.FS(s.webFS))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
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
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
		})
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{
				"name":    "s2l",
				"message": "API only; embed web/dist for UI",
			})
		})
	}

	return s.withAuth(mux)
}

func (s *Server) withAuth(next http.Handler) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			w.Header().Set("WWW-Authenticate", `Basic realm="s2l", charset="UTF-8"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

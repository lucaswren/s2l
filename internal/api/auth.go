package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"time"
)

const sessionCookie = "s2l_session"
const sessionLifetime = 12 * time.Hour
const sessionIdle = 30 * time.Minute

type webSession struct {
	credentials   [32]byte
	created, seen time.Time
}
type loginAttempt struct {
	start    time.Time
	failures int
}

func equalCredential(a, b string) bool {
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}

// authMu protects credentials, sessions and bounded login throttling state.
func (s *Server) credentialKey() [32]byte {
	return sha256.Sum256([]byte(s.adminUser + "\x00" + s.adminPass))
}

func (s *Server) authenticated(r *http.Request) bool {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.adminUser == "" || s.adminPass == "" {
		return true
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || len(cookie.Value) != 64 {
		return false
	}
	key := sha256.Sum256([]byte(cookie.Value))
	session, ok := s.sessions[key]
	now := time.Now()
	if !ok {
		return false
	}
	if session.credentials != s.credentialKey() || now.Sub(session.created) >= sessionLifetime || now.Sub(session.seen) >= sessionIdle {
		delete(s.sessions, key)
		return false
	}
	session.seen = now
	s.sessions[key] = session
	return true
}

func authMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if !authMethod(w, r, http.MethodGet) {
		return
	}
	ok := s.authenticated(r)
	s.authMu.RLock()
	defer s.authMu.RUnlock()
	user := ""
	if ok {
		user = s.adminUser
	}
	writeJSON(w, 200, map[string]any{"enabled": s.adminUser != "" && s.adminPass != "", "authenticated": ok, "username": user})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, age int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: age,
		HttpOnly: true, Secure: secureSettingsRequest(r), SameSite: http.SameSiteStrictMode})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !authMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if decodeJSON(r, &body) != nil || len(body.Username) > 256 || len(body.Password) > 1024 {
		writeError(w, 400, "invalid login request")
		return
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	// The managed HTTPS proxy overwrites X-Forwarded-For with a single IP.
	// Never trust forwarded addresses from a non-loopback client or a chain.
	if address := net.ParseIP(peer); address != nil && address.IsLoopback() {
		if forwarded := net.ParseIP(r.Header.Get("X-Forwarded-For")); forwarded != nil {
			peer = forwarded.String()
		}
	}
	now := time.Now()
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.adminUser == "" || s.adminPass == "" {
		writeJSON(w, 200, map[string]bool{"authenticated": true})
		return
	}
	if s.loginAttempts == nil {
		s.loginAttempts = make(map[string]loginAttempt)
	}
	for ip, attempt := range s.loginAttempts {
		if now.Sub(attempt.start) >= time.Minute {
			delete(s.loginAttempts, ip)
		}
	}
	attempt := s.loginAttempts[peer]
	if attempt.failures >= 5 || (attempt.failures == 0 && len(s.loginAttempts) >= 256) {
		w.Header().Set("Retry-After", "60")
		writeError(w, 429, "too many login attempts; try again in one minute")
		return
	}
	u, p := equalCredential(body.Username, s.adminUser), equalCredential(body.Password, s.adminPass)
	if !u || !p {
		if attempt.failures == 0 {
			attempt.start = now
		}
		attempt.failures++
		s.loginAttempts[peer] = attempt
		writeError(w, 401, "invalid username or password")
		return
	}
	delete(s.loginAttempts, peer)
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		writeError(w, 500, "unable to create session")
		return
	}
	token := hex.EncodeToString(random[:])
	if s.sessions == nil {
		s.sessions = make(map[[32]byte]webSession)
	}
	for key, session := range s.sessions {
		if session.credentials != s.credentialKey() || now.Sub(session.created) >= sessionLifetime || now.Sub(session.seen) >= sessionIdle {
			delete(s.sessions, key)
		}
	}
	// Bound memory; evict the least recently used session when full.
	if len(s.sessions) >= 128 {
		var oldest [32]byte
		var seen time.Time
		for key, session := range s.sessions {
			if seen.IsZero() || session.seen.Before(seen) {
				oldest, seen = key, session.seen
			}
		}
		delete(s.sessions, oldest)
	}
	if previous, err := r.Cookie(sessionCookie); err == nil {
		delete(s.sessions, sha256.Sum256([]byte(previous.Value)))
	}
	s.sessions[sha256.Sum256([]byte(token))] = webSession{credentials: s.credentialKey(), created: now, seen: now}
	s.setSessionCookie(w, r, token, int(sessionLifetime.Seconds()))
	writeJSON(w, 200, map[string]bool{"authenticated": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !authMethod(w, r, http.MethodPost) {
		return
	}
	s.authMu.Lock()
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		delete(s.sessions, sha256.Sum256([]byte(cookie.Value)))
	}
	s.authMu.Unlock()
	s.setSessionCookie(w, r, "", -1)
	writeJSON(w, 200, map[string]bool{"authenticated": false})
}

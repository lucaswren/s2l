package api

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func authRequest(handler http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://panel.example"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestWebLoginProtectsAPIAndRevokesSessions(t *testing.T) {
	s := NewServer(nil, nil, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("login shell")}}, "admin", "long-secret-password")
	h := s.Handler()
	if w := authRequest(h, "GET", "/", "", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	protected := s.withAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	if w := authRequest(protected, "GET", "/api/status", "", nil); w.Code != 401 || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatal(w.Code, w.Header())
	}
	status := authRequest(h, "GET", "/api/auth/status", "", nil)
	if !strings.Contains(status.Body.String(), `"authenticated":false`) || strings.Contains(status.Body.String(), `"username":"admin"`) {
		t.Fatal(status.Body.String())
	}
	bad := authRequest(h, "POST", "/api/auth/login", `{"username":"admin","password":"wrong"}`, nil)
	if bad.Code != 401 || len(bad.Result().Cookies()) != 0 {
		t.Fatal(bad.Code)
	}
	w := authRequest(h, "POST", "/api/auth/login", `{"username":"admin","password":"long-secret-password"}`, nil)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Domain != "" || cookie.Path != "/" || len(cookie.Value) != 64 {
		t.Fatal(cookie)
	}
	if strings.Contains(w.Body.String(), cookie.Value) || strings.Contains(w.Body.String(), "secret") {
		t.Fatal("credentials exposed")
	}
	if got := authRequest(protected, "GET", "/api/status", "", cookie); got.Code != 204 {
		t.Fatal(got.Code)
	}
	// Successful login rotates the presented session rather than fixing it.
	rotated := authRequest(h, "POST", "/api/auth/login", `{"username":"admin","password":"long-secret-password"}`, cookie).Result().Cookies()[0]
	if authRequest(protected, "GET", "/api/status", "", cookie).Code != 401 {
		t.Fatal("old session survived rotation")
	}
	if authRequest(protected, "GET", "/api/status", "", rotated).Code != 204 {
		t.Fatal("new session rejected")
	}
	logout := authRequest(h, "POST", "/api/auth/logout", `{}`, rotated)
	if logout.Code != 200 || logout.Result().Cookies()[0].MaxAge != -1 || authRequest(protected, "GET", "/api/status", "", rotated).Code != 401 {
		t.Fatal("logout did not revoke session")
	}
	// Authorization headers cannot bypass session authentication.
	r := httptest.NewRequest("GET", "https://panel.example/api/status", nil)
	r.SetBasicAuth("admin", "long-secret-password")
	w = httptest.NewRecorder()
	protected.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("Basic credentials bypassed session authentication")
	}
	login := authRequest(h, "POST", "/api/auth/login", `{"username":"admin","password":"long-secret-password"}`, nil)
	r.AddCookie(login.Result().Cookies()[0])
	w = httptest.NewRecorder()
	protected.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("session rejected with unrelated Authorization header")
	}
}

func TestSessionExpiryCredentialChangesAndBounds(t *testing.T) {
	s := NewServer(nil, nil, nil, "admin", "secret")
	h := s.Handler()
	login := func() *http.Cookie {
		t.Helper()
		w := authRequest(h, "POST", "/api/auth/login", `{"username":"admin","password":"secret"}`, nil)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		return w.Result().Cookies()[0]
	}
	for _, kind := range []string{"idle", "absolute", "credentials"} {
		cookie := login()
		key := sha256.Sum256([]byte(cookie.Value))
		session := s.sessions[key]
		switch kind {
		case "idle":
			session.seen = time.Now().Add(-sessionIdle)
		case "absolute":
			session.created = time.Now().Add(-sessionLifetime)
		case "credentials":
			session.credentials = [32]byte{}
		}
		s.sessions[key] = session
		r := httptest.NewRequest("GET", "/api/status", nil)
		r.AddCookie(cookie)
		if s.authenticated(r) {
			t.Fatalf("accepted expired %s session", kind)
		}
	}
	for i := 0; i < 140; i++ {
		login()
	}
	if len(s.sessions) != 128 {
		t.Fatal(len(s.sessions))
	}
	cookie := login()
	s.adminPass = "changed"
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(cookie)
	if s.authenticated(r) {
		t.Fatal("old credentials retained session")
	}
}

func TestLoginCSRFThrottlingAndValidation(t *testing.T) {
	s := NewServer(nil, nil, nil, "admin", "secret")
	h := s.Handler()
	for _, origin := range []string{"https://evil.example", "null"} {
		r := httptest.NewRequest("POST", "https://panel.example/api/auth/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	for _, path := range []string{"login", "logout"} {
		if w := authRequest(h, "GET", "/api/auth/"+path, "", nil); w.Code != 405 {
			t.Fatal(path, w.Code)
		}
	}
	if w := authRequest(h, "POST", "/api/auth/login", `{"username":"admin","password":"secret","extra":true}`, nil); w.Code != 400 {
		t.Fatal(w.Code)
	}
	for i := 0; i < 5; i++ {
		if w := authRequest(h, "POST", "/api/auth/login", `{"username":"admin","password":"bad"}`, nil); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	w := authRequest(h, "POST", "/api/auth/login", `{"username":"admin","password":"secret"}`, nil)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal(w.Code)
	}
	for ip, a := range s.loginAttempts {
		a.start = time.Now().Add(-time.Minute)
		s.loginAttempts[ip] = a
	}
	if w := authRequest(h, "POST", "/api/auth/login", `{"username":"admin","password":"secret"}`, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	// Disabled credentials preserve the explicitly configured local API mode.
	s = NewServer(nil, nil, nil)
	w = authRequest(s.Handler(), "GET", "/api/auth/status", "", nil)
	var status map[string]any
	if json.Unmarshal(w.Body.Bytes(), &status) != nil || status["enabled"] != false || status["authenticated"] != true {
		t.Fatal(w.Body.String())
	}
}

func TestLoginThrottleTrustsOnlySingleLoopbackProxyAddress(t *testing.T) {
	s := NewServer(nil, nil, nil, "admin", "secret")
	for _, tc := range []struct{ remote, forwarded, want string }{
		{"127.0.0.1:1234", "192.0.2.8", "192.0.2.8"},
		{"192.0.2.7:1234", "192.0.2.9", "192.0.2.7"},
		{"127.0.0.1:1234", "192.0.2.8, 192.0.2.9", "127.0.0.1"},
	} {
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"bad"}`))
		r.RemoteAddr = tc.remote
		r.Header.Set("X-Forwarded-For", tc.forwarded)
		w := httptest.NewRecorder()
		s.handleLogin(w, r)
		if s.loginAttempts[tc.want].failures != 1 {
			t.Fatal(tc, s.loginAttempts)
		}
	}
}

func testLogin(t *testing.T, handler http.Handler, user, password string) *http.Cookie {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": user, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	response := authRequest(handler, "POST", "/api/auth/login", string(body), nil)
	if response.Code != 200 || len(response.Result().Cookies()) != 1 {
		t.Fatalf("login failed: HTTP %d", response.Code)
	}
	return response.Result().Cookies()[0]
}

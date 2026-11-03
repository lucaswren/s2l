package api

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucaswren/s2l/internal/store"
)

func TestBackupCleanupRequiresSessionAndSameOrigin(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateBackup()
	if err != nil {
		t.Fatal(err)
	}
	h := NewServer(st, nil, nil, "admin", "cleanup-password-123").Handler()
	cookie := testLogin(t, h, "admin", "cleanup-password-123")
	for _, tc := range []struct {
		auth   bool
		origin string
		code   int
	}{{false, "", 401}, {true, "https://foreign.example", 403}, {true, "", 200}} {
		r := httptest.NewRequest("DELETE", "https://admin.example/api/backups", strings.NewReader(`{"ids":["`+b.ID+`"]}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", tc.origin)
		if tc.auth {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.code != 200 {
			if _, err := st.ReadBackup(b.ID); err != nil {
				t.Fatal("unauthorized deletion", err)
			}
		}
	}
}

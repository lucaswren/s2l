package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/service"
	"github.com/lucaswren/s2l/internal/store"
)

func TestBackupPreviewRestoreAndStaleRevision(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	n := model.SocksNode{ID: "n", Name: "original", Addr: "127.0.0.1:1080", Password: "do-not-disclose"}
	if err = st.UpsertNode(n); err != nil {
		t.Fatal(err)
	}
	m := model.TunnelMapping{ID: "m", NodeID: "n", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "user", L2TPPass: "private-password", Status: model.StatusStopped}
	m.DNSDoHURL, m.DNSTCPAddress = "https://resolver.example/dns-query", "dns.internal:5353"
	if err = st.UpsertMapping(m); err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateBackup()
	if err != nil {
		t.Fatal(err)
	}
	n.Name = "changed"
	st.UpsertNode(n)
	changed := m
	changed.DNSTCPAddress = "other.internal:53"
	if err := st.UpsertMapping(changed); err != nil {
		t.Fatal(err)
	}
	h := NewServer(st, service.NewMappingService(st, nil, nil, nil), nil, "admin", "admin-password-long").Handler()
	post := func(path string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(testLogin(t, h, "admin", "admin-password-long"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	preview := post("/api/backups/preview", map[string]string{"id": b.ID})
	if preview.Code != 200 {
		t.Fatal(preview.Code, preview.Body.String())
	}
	if bytes.Contains(preview.Body.Bytes(), []byte(n.Password)) || bytes.Contains(preview.Body.Bytes(), []byte(m.L2TPPass)) {
		t.Fatal("preview disclosed credentials")
	}
	var p struct {
		Revision       string `json:"revision"`
		BackupRevision string `json:"backup_revision"`
		CanRestore     bool   `json:"can_restore"`
	}
	json.Unmarshal(preview.Body.Bytes(), &p)
	if !p.CanRestore {
		t.Fatal("stopped set cannot restore")
	}
	body := restoreRequest{ID: b.ID, Revision: p.Revision, BackupRevision: p.BackupRevision}
	n.Name = "changed-again"
	st.UpsertNode(n)
	if w := post("/api/backups/restore", body); w.Code != 409 {
		t.Fatal("stale preview accepted", w.Code, w.Body.String())
	}
	preview = post("/api/backups/preview", map[string]string{"id": b.ID})
	json.Unmarshal(preview.Body.Bytes(), &p)
	body.Revision = p.Revision
	if w := post("/api/backups/restore", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	got, _ := st.GetNode("n")
	if got.Name != "original" {
		t.Fatal("wrong rollback")
	}
	gotMapping, _ := st.GetMapping("m")
	if gotMapping.Status != model.StatusStopped {
		t.Fatal("restored active mapping")
	}
	if gotMapping.EffectiveDNSOptions() != m.EffectiveDNSOptions() {
		t.Fatal("backup lost custom DNS", gotMapping)
	}
	r := httptest.NewRequest("GET", "/api/backups", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("backup list was unauthenticated")
	}
}

func TestInvalidBackupReferencesAndConflictsRejected(t *testing.T) {
	n := model.SocksNode{ID: "n", Addr: "127.0.0.1:1080"}
	m := model.TunnelMapping{ID: "m", NodeID: "n", TunName: "tun101", TableID: 101, Subnet: "10.0.101.0/24", L2TPUser: "user", L2TPPass: "password"}
	other := m
	other.ID = "m2"
	other.TunName = "tun102"
	other.TableID = 102
	other.L2TPUser = "user2"
	for _, state := range []model.AppState{
		{Nodes: []model.SocksNode{}, Mappings: []model.TunnelMapping{m}},
		{Nodes: []model.SocksNode{n, n}, Mappings: []model.TunnelMapping{}},
		{Nodes: []model.SocksNode{n}, Mappings: []model.TunnelMapping{m, other}},
	} {
		if err := validateBackup(state); err == nil {
			t.Fatal("invalid backup accepted")
		}
	}
}

type decodedBody struct {
	io.ReadCloser
	closed chan struct{}
	once   sync.Once
}

func (b *decodedBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return b.ReadCloser.Close()
}

func TestCanceledRollbackWaitingForLifecycleDoesNotCommit(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	n := model.SocksNode{ID: "n", Name: "original", Addr: "127.0.0.1:1080"}
	if err = st.UpsertNode(n); err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateBackup()
	if err != nil {
		t.Fatal(err)
	}
	target, _ := st.ReadBackup(b.ID)
	n.Name = "current"
	st.UpsertNode(n)
	svc := service.NewMappingService(st, nil, nil, nil)
	s := NewServer(st, svc, nil)
	entered, release, gateDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	go func() { defer close(gateDone); svc.SynchronizeState(func() { close(entered); <-release }) }()
	<-entered
	body, _ := json.Marshal(restoreRequest{ID: b.ID, Revision: store.Revision(st.Snapshot()), BackupRevision: store.Revision(target.State)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/api/backups/restore", bytes.NewReader(body)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	signal := &decodedBody{ReadCloser: r.Body, closed: make(chan struct{})}
	r.Body = signal
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); s.Handler().ServeHTTP(w, r) }()
	select {
	case <-signal.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not decode")
	}
	cancel()
	unblock()
	<-gateDone
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled rollback remained blocked")
	}
	if w.Code != 408 {
		t.Fatal("canceled queued rollback accepted", w.Code, w.Body.String())
	}
	got, _ := st.GetNode("n")
	if got.Name != "current" {
		t.Fatal("rollback committed after request cancellation")
	}
}

func TestRollbackDistinguishesActiveConflictAndPersistenceFailure(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			primary := filepath.Join(t.TempDir(), "state.json")
			st, err := store.New(primary)
			if err != nil {
				t.Fatal(err)
			}
			n := model.SocksNode{ID: "n", Name: "old", Addr: "127.0.0.1:1080"}
			st.UpsertNode(n)
			b, err := st.CreateBackup()
			if err != nil {
				t.Fatal(err)
			}
			target, _ := st.ReadBackup(b.ID)
			n.Name = "current"
			st.UpsertNode(n)
			if active {
				st.UpsertMapping(model.TunnelMapping{ID: "active", Status: model.StatusRunning})
			} else {
				if err = os.Remove(primary); err != nil {
					t.Fatal(err)
				}
				if err = os.Mkdir(primary, 0700); err != nil {
					t.Fatal(err)
				}
			}
			body, _ := json.Marshal(restoreRequest{ID: b.ID, Revision: store.Revision(st.Snapshot()), BackupRevision: store.Revision(target.State)})
			r := httptest.NewRequest("POST", "/api/backups/restore", bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			NewServer(st, nil, nil).Handler().ServeHTTP(w, r)
			want := 500
			if active {
				want = 409
			}
			if w.Code != want {
				t.Fatal("incorrect rollback error classification", w.Code, w.Body.String())
			}
			got, _ := st.GetNode("n")
			if got.Name != "current" {
				t.Fatal("failed rollback changed memory")
			}
		})
	}
}

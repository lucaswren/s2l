package api

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sort"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
	"github.com/lucaswren/s2l/internal/validation"
)

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListBackups()
		if err != nil {
			writeError(w, 500, "cannot list backups")
			return
		}
		writeJSON(w, 200, items)
	case http.MethodPost:
		item, err := s.store.CreateBackup()
		if err != nil {
			writeError(w, 500, "cannot save backup")
			return
		}
		writeJSON(w, 200, item)
	case http.MethodDelete:
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var body struct {
			IDs []string `json:"ids"`
		}
		if decodeJSON(r, &body) != nil || len(body.IDs) == 0 || len(body.IDs) > 10 {
			writeError(w, 400, "invalid backup selection")
			return
		}
		s.mutationMu.Lock()
		defer s.mutationMu.Unlock()
		if err := s.store.DeleteBackups(body.IDs); err != nil {
			writeError(w, 409, "备份清理失败，请刷新列表后重试")
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": len(body.IDs)})
	default:
		w.WriteHeader(405)
	}
}

type restoreRequest struct {
	ID             string `json:"id"`
	Revision       string `json:"revision"`
	BackupRevision string `json:"backup_revision"`
}

func validateBackup(state model.AppState) error { return validation.State(state) }

type configChange struct {
	Kind               string `json:"kind"`
	ID                 string `json:"id"`
	Change             string `json:"change"`
	Before             any    `json:"before,omitempty"`
	After              any    `json:"after,omitempty"`
	CredentialsChanged bool   `json:"credentials_changed"`
}

func previewValue(v any) any {
	switch value := v.(type) {
	case model.SocksNode:
		return redactNode(value)
	case model.TunnelMapping:
		return redactMapping(value)
	}
	return nil
}
func previewPassword(v any) string {
	switch value := v.(type) {
	case model.SocksNode:
		return value.Password
	case model.TunnelMapping:
		return value.L2TPPass
	}
	return ""
}

func backupChanges(current, target model.AppState) []configChange {
	out := []configChange{}
	a, b := map[string]any{}, map[string]any{}
	for _, n := range current.Nodes {
		a["node/"+n.ID] = n
	}
	for _, n := range target.Nodes {
		b["node/"+n.ID] = n
	}
	for _, m := range current.Mappings {
		m.Status = ""
		m.ErrorMsg = ""
		a["mapping/"+m.ID] = m
	}
	for _, m := range target.Mappings {
		m.Status = ""
		m.ErrorMsg = ""
		b["mapping/"+m.ID] = m
	}
	add := func(key, change string) {
		kind, id := "node", key[5:]
		if len(key) >= 8 && key[:8] == "mapping/" {
			kind, id = "mapping", key[8:]
		}
		out = append(out, configChange{Kind: kind, ID: id, Change: change, Before: previewValue(a[key]), After: previewValue(b[key]), CredentialsChanged: previewPassword(a[key]) != previewPassword(b[key])})
	}
	for id, v := range a {
		if n, ok := b[id]; !ok {
			add(id, "remove")
		} else if !reflect.DeepEqual(v, n) {
			add(id, "update")
		}
	}
	for id := range b {
		if _, ok := a[id]; !ok {
			add(id, "add")
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].ID < out[j].ID
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var body restoreRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	// Serializes with state edits and excludes startup/recovery, including its validation window.
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	apply := func() {
		if r.Context().Err() != nil {
			writeError(w, 408, "rollback request canceled before commit")
			return
		}
		b, err := s.store.ReadBackup(body.ID)
		if err != nil {
			writeError(w, 400, "backup missing or invalid")
			return
		}
		if err = validateBackup(b.State); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		current := s.store.Snapshot()
		if r.URL.Path == "/api/backups/preview" {
			stopped := true
			for _, m := range current.Mappings {
				if m.Status != model.StatusStopped {
					stopped = false
				}
			}
			writeJSON(w, 200, map[string]any{"revision": store.Revision(current), "backup_revision": store.Revision(b.State), "changes": backupChanges(current, store.RestoreTarget(b.State, current.NetworkPolicyVersion)), "can_restore": stopped, "scope": "nodes and mappings; system account, SSH, IPsec and listener settings are unchanged"})
			return
		}
		if body.BackupRevision == "" || store.Revision(b.State) != body.BackupRevision {
			writeError(w, 409, "backup changed; preview again")
			return
		}
		if err = s.store.RestoreContext(r.Context(), b.State, body.Revision); err != nil {
			code := 500
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				code = 408
			} else if errors.Is(err, store.ErrChanged) || errors.Is(err, store.ErrActive) {
				code = 409
			}
			writeError(w, code, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"message": "配置已恢复；所有映射保持停止，请按需启动"})
	}
	if s.svc != nil {
		s.svc.SynchronizeState(apply)
	} else {
		apply()
	}
}

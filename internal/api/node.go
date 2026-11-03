package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/store"
)

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, redactNodes(s.store.ListNodes()))
	case http.MethodPost:
		s.createOrUpdateNode(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleNodeByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	id := parts[0]

	if len(parts) == 2 && parts[1] == "network-test" {
		s.handleNodeNetworkTest(w, r, id)
		return
	}
	// POST /api/nodes/{id}/speedtest
	if len(parts) == 2 && parts[1] == "speedtest" {
		s.handleNodeSpeedTest(w, r, id)
		return
	}
	if len(parts) != 1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		n, ok := s.store.GetNode(id)
		if !ok {
			writeError(w, http.StatusNotFound, "node not found")
			return
		}
		writeResource(w, n)
	case http.MethodDelete:
		if err := s.store.DeleteNode(id); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) createOrUpdateNode(w http.ResponseWriter, r *http.Request) {
	var n model.SocksNode
	if err := decodeJSON(r, &n); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	n.Name = strings.TrimSpace(n.Name)
	n.Addr = strings.TrimSpace(n.Addr)
	if err := validateSocksNode(n); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if n.Name == "" {
		n.Name = n.Addr
	}
	if n.ID == "" {
		n.ID = newID("node_")
	}
	old, exists := s.store.GetNode(n.ID)
	if !exists && r.Header.Get("If-Match") != "" {
		writeError(w, 409, "配置已删除，请重新加载列表")
		return
	}
	if exists && !revisionMatches(w, r, old) {
		return
	}
	if !validGroup(n.Group) {
		writeError(w, 400, "分组名称无效")
		return
	}
	if s.previewChange(w, r, old, n, n.ID) {
		return
	}
	if err := s.store.UpsertNode(n); err != nil {
		if errors.Is(err, store.ErrNodeInUse) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, redactNode(n))
}

package api

import (
	"net/http"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
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
		writeJSON(w, http.StatusOK, n)
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
	if err := s.store.UpsertNode(n); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, redactNode(n))
}

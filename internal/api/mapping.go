package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/lucaswren/s2l/internal/model"
)

type mappingAction struct {
	Action string `json:"action"` // start | stop
}

func (s *Server) handleMappings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, redactMappings(s.store.ListMappings()))
	case http.MethodPost:
		s.createOrUpdateMapping(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMappingByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/mappings/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "invalid mapping id")
		return
	}
	id := parts[0]

	// POST /api/mappings/{id}/action  { "action": "start"|"stop" }
	if len(parts) == 2 && parts[1] == "action" && r.Method == http.MethodPost {
		var body mappingAction
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		var err error
		switch body.Action {
		case "start":
			err = s.svc.Start(id)
		case "stop":
			err = s.svc.Stop(id)
		default:
			writeError(w, http.StatusBadRequest, "action must be start or stop")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		m, _ := s.store.GetMapping(id)
		writeJSON(w, http.StatusOK, redactMapping(m))
		return
	}

	if len(parts) != 1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		m, ok := s.store.GetMapping(id)
		if !ok {
			writeError(w, http.StatusNotFound, "mapping not found")
			return
		}
		writeJSON(w, http.StatusOK, m)
	case http.MethodDelete:
		m, ok := s.store.GetMapping(id)
		if !ok {
			writeError(w, http.StatusNotFound, "mapping not found")
			return
		}
		if m.Status != model.StatusStopped {
			if err := s.svc.Stop(id); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if err := s.store.DeleteMapping(id); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) createOrUpdateMapping(w http.ResponseWriter, r *http.Request) {
	var m model.TunnelMapping
	if err := decodeJSON(r, &m); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	m.TunName = strings.TrimSpace(m.TunName)
	m.Subnet = strings.TrimSpace(m.Subnet)
	m.L2TPUser = strings.TrimSpace(m.L2TPUser)
	m.NodeID = strings.TrimSpace(m.NodeID)

	if err := validateMapping(m); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := s.store.GetNode(m.NodeID); !ok {
		writeError(w, http.StatusBadRequest, "node not found")
		return
	}
	if err := s.checkMappingConflicts(m); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	isNew := m.ID == ""
	if isNew {
		m.ID = newID("map_")
		m.Status = model.StatusStopped
	} else {
		existing, ok := s.store.GetMapping(m.ID)
		if ok {
			if existing.Status == model.StatusRunning {
				changed := existing.NodeID != m.NodeID ||
					existing.TunName != m.TunName ||
					existing.TableID != m.TableID ||
					existing.Subnet != m.Subnet || existing.L2TPUser != m.L2TPUser || existing.L2TPPass != m.L2TPPass
				if changed {
					writeError(w, http.StatusBadRequest, "mapping is running; stop it before changing any settings")
					return
				}
			}
			m.Status = existing.Status
			m.ErrorMsg = existing.ErrorMsg
		} else {
			m.Status = model.StatusStopped
		}
	}

	if err := s.store.UpsertMapping(m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, redactMapping(m))
}

func (s *Server) checkMappingConflicts(m model.TunnelMapping) error {
	for _, other := range s.store.ListMappings() {
		if other.ID == m.ID {
			continue
		}
		if other.TunName == m.TunName {
			return fmt.Errorf("tun_name %s already used by %s", m.TunName, other.ID)
		}
		if other.TableID == m.TableID {
			return fmt.Errorf("table_id %d already used by %s", m.TableID, other.ID)
		}
		if subnetsOverlap(other.Subnet, m.Subnet) {
			return fmt.Errorf("subnet %s overlaps mapping %s (%s)", m.Subnet, other.ID, other.Subnet)
		}
		if other.L2TPUser != "" && other.L2TPUser == m.L2TPUser {
			return fmt.Errorf("l2tp_user %s already used by %s", m.L2TPUser, other.ID)
		}
	}
	return nil
}

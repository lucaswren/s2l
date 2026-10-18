package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

// Store 线程安全的 JSON 持久化存储
type Store struct {
	path           string
	mu             sync.RWMutex
	state          model.AppState
	recoveryNotice string
	now            func() time.Time   // optional deterministic clock for persistence tests
	syncDir        func(string) error // optional I/O fault injection; nil uses OS fsync
}

func (s *Store) DataDir() string        { return filepath.Dir(s.path) }
func (s *Store) RecoveryNotice() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.recoveryNotice }

func New(path string) (*Store, error) {
	s := &Store{path: path}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := readPrimary(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.state = model.AppState{
				Nodes:    []model.SocksNode{},
				Mappings: []model.TunnelMapping{},
			}
			return nil
		}
		return err
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return s.recoverCorruptState(data)
	}
	if err := json.Unmarshal(data, &s.state); err != nil {
		if recoveryErr := s.recoverCorruptState(data); recoveryErr != nil {
			return fmt.Errorf("parse state: %w; backup recovery: %v", err, recoveryErr)
		}
		return nil
	}
	if s.state.Nodes == nil {
		s.state.Nodes = []model.SocksNode{}
	}
	if s.state.Mappings == nil {
		s.state.Mappings = []model.TunnelMapping{}
	}
	return nil
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	// Keep every committed configuration small enough for its versioned backup
	// envelope, including indentation. Reject growth before it can block all
	// subsequent edits (including attempts to shrink an oversized state).
	if len(data) > 4<<20 {
		return fmt.Errorf("configuration exceeds the supported 4 MiB size limit")
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".s2l-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	if err := s.syncDirectory(filepath.Dir(s.path)); err != nil {
		return &committedError{err}
	}
	return nil
}

func (s *Store) syncDirectory(path string) error {
	if s.syncDir != nil {
		return s.syncDir(path)
	}
	return syncDirectory(path)
}
func readPrimary(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	i, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !i.Mode().IsRegular() || i.Size() > maxBackupBytes {
		return nil, fmt.Errorf("state file must be regular and at most 8 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBackupBytes+1))
	if err == nil && len(data) > maxBackupBytes {
		err = fmt.Errorf("state file grew beyond 8 MiB")
	}
	return data, err
}

func cloneState(in model.AppState) model.AppState {
	out := model.AppState{
		Nodes:    make([]model.SocksNode, len(in.Nodes)),
		Mappings: make([]model.TunnelMapping, len(in.Mappings)),
	}
	copy(out.Nodes, in.Nodes)
	copy(out.Mappings, in.Mappings)
	return out
}

func (s *Store) saveOrRestoreLocked(previous model.AppState) error {
	if configurationChanged(previous, s.state) {
		if _, err := s.createBackupLocked(previous); err != nil {
			s.state = previous
			return fmt.Errorf("backup configuration: %w", err)
		}
	}
	if err := s.saveLocked(); err != nil {
		var committed *committedError
		if !errors.As(err, &committed) {
			s.state = previous
		}
		return err
	}
	return nil
}

func (s *Store) Snapshot() model.AppState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// 必须返回非 nil 空切片，避免 JSON 编码成 null 导致前端崩溃
	nodes := make([]model.SocksNode, 0, len(s.state.Nodes))
	nodes = append(nodes, s.state.Nodes...)
	mappings := make([]model.TunnelMapping, 0, len(s.state.Mappings))
	mappings = append(mappings, s.state.Mappings...)
	return model.AppState{Nodes: nodes, Mappings: mappings}
}

// --- Nodes ---

func (s *Store) ListNodes() []model.SocksNode {
	return s.Snapshot().Nodes
}

func (s *Store) GetNode(id string) (model.SocksNode, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, n := range s.state.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return model.SocksNode{}, false
}

func (s *Store) UpsertNode(n model.SocksNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := cloneState(s.state)
	for i, existing := range s.state.Nodes {
		if existing.ID == n.ID {
			if existing.Addr != n.Addr || existing.Username != n.Username || existing.Password != n.Password {
				for _, m := range s.state.Mappings {
					if m.NodeID == n.ID && m.Status == model.StatusRunning {
						return fmt.Errorf("node is used by a running mapping; stop it before changing connection settings")
					}
				}
			}
			s.state.Nodes[i] = n
			return s.saveOrRestoreLocked(previous)
		}
	}
	s.state.Nodes = append(s.state.Nodes, n)
	return s.saveOrRestoreLocked(previous)
}

func (s *Store) DeleteNode(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := cloneState(s.state)
	for _, m := range s.state.Mappings {
		if m.NodeID == id {
			return fmt.Errorf("node %s is used by mapping %s", id, m.ID)
		}
	}
	out := s.state.Nodes[:0]
	found := false
	for _, n := range s.state.Nodes {
		if n.ID == id {
			found = true
			continue
		}
		out = append(out, n)
	}
	if !found {
		return fmt.Errorf("node %s not found", id)
	}
	s.state.Nodes = out
	return s.saveOrRestoreLocked(previous)
}

// --- Mappings ---

func (s *Store) ListMappings() []model.TunnelMapping {
	return s.Snapshot().Mappings
}

func (s *Store) GetMapping(id string) (model.TunnelMapping, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.state.Mappings {
		if m.ID == id {
			return m, true
		}
	}
	return model.TunnelMapping{}, false
}

func (s *Store) UpsertMapping(m model.TunnelMapping) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := cloneState(s.state)
	for i, existing := range s.state.Mappings {
		if existing.ID == m.ID {
			s.state.Mappings[i] = m
			return s.saveOrRestoreLocked(previous)
		}
	}
	s.state.Mappings = append(s.state.Mappings, m)
	return s.saveOrRestoreLocked(previous)
}

func (s *Store) DeleteMapping(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := cloneState(s.state)
	out := s.state.Mappings[:0]
	found := false
	for _, m := range s.state.Mappings {
		if m.ID == id {
			found = true
			continue
		}
		out = append(out, m)
	}
	if !found {
		return fmt.Errorf("mapping %s not found", id)
	}
	s.state.Mappings = out
	return s.saveOrRestoreLocked(previous)
}

func (s *Store) UpdateMappingStatus(id, status, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := cloneState(s.state)
	for i, m := range s.state.Mappings {
		if m.ID == id {
			s.state.Mappings[i].Status = status
			s.state.Mappings[i].ErrorMsg = errMsg
			return s.saveOrRestoreLocked(previous)
		}
	}
	return fmt.Errorf("mapping %s not found", id)
}

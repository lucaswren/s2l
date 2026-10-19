package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/validation"
)

const backupLimit = 10
const maxBackupBytes = 8 << 20

var backupName = regexp.MustCompile(`^backup-[0-9]+-[0-9]+\.json$`)
var ErrChanged = errors.New("configuration changed; preview again")
var ErrActive = errors.New("stop all mappings before restoring")

// Rename committed the new state. Keep memory consistent even if the subsequent
// directory fsync fails, and surface the durability failure to the operator.
type committedError struct{ error }

func (e *committedError) Error() string {
	return "configuration saved but directory sync failed: " + e.error.Error()
}
func (e *committedError) Unwrap() error { return e.error }

type Backup struct {
	Version   int            `json:"version"`
	CreatedAt time.Time      `json:"created_at"`
	State     model.AppState `json:"state"`
	Checksum  string         `json:"checksum"`
}
type BackupInfo struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Nodes     int       `json:"nodes"`
	Mappings  int       `json:"mappings"`
}

// Backups contain credentials. Keep them outside log bundles in a private directory.
func (s *Store) backupDir() (string, error) {
	dir := filepath.Join(filepath.Dir(s.path), "backups")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	i, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("backup directory must be a real directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func stoppedState(state model.AppState) model.AppState {
	out := cloneState(state)
	for i := range out.Mappings {
		out.Mappings[i].Status = model.StatusStopped
		out.Mappings[i].ErrorMsg = ""
	}
	return out
}

func Revision(state model.AppState) string {
	b, _ := json.Marshal(state)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Store) createBackupLocked(state model.AppState) (BackupInfo, error) {
	dir, err := s.backupDir()
	if err != nil {
		return BackupInfo{}, err
	}
	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	// Order publications monotonically even when the system clock goes back.
	// Retention must never discard the just-created pre-rollback undo snapshot.
	stamp := uint64(0)
	if now.UnixNano() > 0 {
		stamp = uint64(now.UnixNano())
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return BackupInfo{}, err
	}
	for _, f := range files {
		if n, valid := backupOrdinal(f.Name()); valid && n >= stamp {
			if n == ^uint64(0) {
				return BackupInfo{}, fmt.Errorf("backup sequence exhausted")
			}
			stamp = n + 1
		}
	}
	b := Backup{Version: 1, CreatedAt: now.UTC(), State: stoppedState(state)}
	b.Checksum = Revision(b.State)
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return BackupInfo{}, err
	}
	if len(data) > maxBackupBytes {
		return BackupInfo{}, fmt.Errorf("configuration exceeds backup size limit")
	}
	f, err := os.CreateTemp(dir, ".backup-"+fmt.Sprintf("%020d", stamp)+"-*.json")
	if err != nil {
		return BackupInfo{}, err
	}
	ok := false
	finalPath := filepath.Join(dir, strings.TrimPrefix(filepath.Base(f.Name()), "."))
	defer func() {
		f.Close()
		if !ok {
			os.Remove(f.Name())
			os.Remove(finalPath)
		}
	}()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return BackupInfo{}, err
	}
	if err = os.Rename(f.Name(), finalPath); err != nil {
		return BackupInfo{}, err
	}
	if err = s.syncDirectory(dir); err != nil {
		return BackupInfo{}, err
	}
	ok = true
	// Only prune after a complete backup is durable. Ignore foreign files.
	files, err = os.ReadDir(dir)
	if err != nil {
		return BackupInfo{}, err
	}
	names := []string{}
	for _, f := range files {
		if strings.HasPrefix(f.Name(), ".backup-") && backupName.MatchString(strings.TrimPrefix(f.Name(), ".")) && f.Type().IsRegular() {
			if err = os.Remove(filepath.Join(dir, f.Name())); err != nil {
				return BackupInfo{}, err
			}
		}
		if backupName.MatchString(f.Name()) && f.Type().IsRegular() {
			names = append(names, f.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool { return backupOlder(names[i], names[j]) })
	for len(names) > backupLimit {
		if err = os.Remove(filepath.Join(dir, names[0])); err != nil {
			return BackupInfo{}, err
		}
		names = names[1:]
	}
	return BackupInfo{filepath.Base(finalPath), b.CreatedAt, len(b.State.Nodes), len(b.State.Mappings)}, nil
}

func (s *Store) CreateBackup() (BackupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createBackupLocked(s.state)
}

func (s *Store) ReadBackup(id string) (Backup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readBackupLocked(id)
}
func (s *Store) readBackupLocked(id string) (Backup, error) {
	if !backupName.MatchString(id) {
		return Backup{}, fmt.Errorf("invalid backup id")
	}
	dir, err := s.backupDir()
	if err != nil {
		return Backup{}, err
	}
	path := filepath.Join(dir, id)
	i, err := os.Lstat(path)
	if err != nil {
		return Backup{}, err
	}
	if !i.Mode().IsRegular() || i.Size() > maxBackupBytes {
		return Backup{}, fmt.Errorf("invalid backup file")
	}
	f, err := os.Open(path)
	if err != nil {
		return Backup{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return Backup{}, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(i, opened) {
		return Backup{}, fmt.Errorf("backup changed during read")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBackupBytes+1))
	if err != nil {
		return Backup{}, err
	}
	var b Backup
	if len(data) > maxBackupBytes {
		return b, fmt.Errorf("backup too large")
	}
	if err = json.Unmarshal(data, &b); err != nil {
		return b, err
	}
	if b.Version != 1 || b.State.Nodes == nil || b.State.Mappings == nil {
		return b, fmt.Errorf("unsupported or incomplete backup")
	}
	if b.Checksum == "" || b.Checksum != Revision(b.State) {
		return b, fmt.Errorf("backup checksum mismatch")
	}
	b.State = stoppedState(b.State)
	return b, nil
}

func (s *Store) ListBackups() ([]BackupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.backupDir()
	if err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []BackupInfo{}
	for _, f := range files {
		if !backupName.MatchString(f.Name()) {
			continue
		}
		b, err := s.readBackupLocked(f.Name())
		if err != nil {
			continue
		}
		out = append(out, BackupInfo{f.Name(), b.CreatedAt, len(b.State.Nodes), len(b.State.Mappings)})
	}
	sort.Slice(out, func(i, j int) bool { return backupOlder(out[j].ID, out[i].ID) })
	return out, nil
}

// Restore only accepts a validated preview and a matching current revision.
// The caller excludes lifecycle work; the store also checks the persisted active set.
func (s *Store) Restore(state model.AppState, expected string) error {
	return s.RestoreContext(context.Background(), state, expected)
}
func (s *Store) RestoreContext(ctx context.Context, state model.AppState, expected string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected == "" || Revision(s.state) != expected {
		return ErrChanged
	}
	for _, m := range s.state.Mappings {
		if m.Status != model.StatusStopped {
			return ErrActive
		}
	}
	if err := validation.State(state); err != nil {
		return fmt.Errorf("invalid restore state: %w", err)
	}
	previous := cloneState(s.state)
	if _, err := s.createBackupLocked(previous); err != nil {
		return fmt.Errorf("backup before rollback: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.state = stoppedState(state)
	// Restoring business configuration must not roll back installed migrations.
	if s.state.NetworkPolicyVersion < previous.NetworkPolicyVersion {
		s.state.NetworkPolicyVersion = previous.NetworkPolicyVersion
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

func backupOrdinal(name string) (uint64, bool) {
	if !backupName.MatchString(name) {
		return 0, false
	}
	part, _, _ := strings.Cut(strings.TrimPrefix(name, "backup-"), "-")
	n, err := strconv.ParseUint(part, 10, 64)
	return n, err == nil
}
func backupOlder(a, b string) bool {
	x, _ := backupOrdinal(a)
	y, _ := backupOrdinal(b)
	if x == y {
		return a < b
	}
	return x < y
}

func configurationChanged(a, b model.AppState) bool {
	return !reflect.DeepEqual(stoppedState(a), stoppedState(b))
}

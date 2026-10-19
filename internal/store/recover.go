package store

import (
	"encoding/json"
	"fmt"
	"github.com/lucaswren/s2l/internal/validation"
	"os"
	"path/filepath"
	"sort"
)

// A malformed primary must not prevent access to the management UI when a valid
// backup exists. Preserve the original privately and recover with all tunnels
// stopped; never automatically activate credentials from an older snapshot.
func (s *Store) recoverCorruptState(original []byte) error {
	if len(original) > maxBackupBytes {
		return fmt.Errorf("corrupt state exceeds safe recovery size limit")
	}
	items, err := s.ListBackups()
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("no valid backup available")
	}
	var b Backup
	found := false
	for _, item := range items {
		candidate, readErr := s.ReadBackup(item.ID)
		if readErr != nil || validation.State(candidate.State) != nil {
			continue
		}
		data, marshalErr := json.MarshalIndent(candidate.State, "", "  ")
		if marshalErr != nil || len(data) > 4<<20 {
			continue
		}
		b = candidate
		found = true
		break
	}
	if !found {
		return fmt.Errorf("no restorable backup available")
	}
	dir, err := s.backupDir()
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "corrupt-*.json")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		f.Close()
		if !keep {
			os.Remove(f.Name())
		}
	}()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(original)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = s.syncDirectory(dir); err != nil {
		return err
	}
	keep = true
	version := s.state.NetworkPolicyVersion
	s.state = b.State
	if s.state.NetworkPolicyVersion < version {
		s.state.NetworkPolicyVersion = version
	}
	if err = s.saveLocked(); err != nil {
		return err
	}
	s.recoveryNotice = "状态文件损坏，已从最近有效备份恢复节点与映射，全部映射保持停止。原损坏文件已在私有备份目录保留，请核对后按需启动。"
	// Bound preserved corrupt copies too. Never match unrelated files.
	paths, _ := filepath.Glob(filepath.Join(dir, "corrupt-*.json"))
	sort.Slice(paths, func(i, j int) bool {
		a, ea := os.Stat(paths[i])
		b, eb := os.Stat(paths[j])
		return ea == nil && (eb != nil || a.ModTime().Before(b.ModTime()))
	})
	for len(paths) > 3 {
		if err = os.Remove(paths[0]); err != nil {
			s.recoveryNotice += " 部分旧损坏副本未能清理，请检查备份目录权限。"
			break
		}
		paths = paths[1:]
	}
	return nil
}

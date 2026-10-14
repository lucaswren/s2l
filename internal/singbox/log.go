package singbox

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/lucaswren/s2l/internal/model"
)

const logLimit int64 = 5 * 1024 * 1024

type rotatingLog struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	size    int64
	limit   int64
	status  model.ProcessStatus
	pending string
}

func openRotatingLog(path string) (*rotatingLog, error) {
	l := &rotatingLog{path: path, limit: logLimit}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}
func (l *rotatingLog) open() error {
	if info, err := os.Lstat(l.path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("log must be a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.file = f
	l.size = info.Size()
	return nil
}
func (l *rotatingLog) rotate() error {
	if err := l.file.Close(); err != nil {
		return err
	}
	l.file = nil
	for i := 2; i >= 1; i-- {
		old, next := fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1)
		if err := os.Remove(next); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(old, next); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil {
		return err
	}
	return l.open()
}
func (l *rotatingLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	total := 0
	for len(p) > 0 {
		if l.file == nil {
			return total, os.ErrClosed
		}
		if l.size >= l.limit {
			if err := l.rotate(); err != nil {
				return total, err
			}
		}
		n := len(p)
		if int64(n) > l.limit-l.size {
			n = int(l.limit - l.size)
		}
		written, err := l.file.Write(p[:n])
		l.size += int64(written)
		total += written
		l.status.LogBytes += uint64(written)
		l.pending += string(p[:written])
		for {
			i := strings.IndexByte(l.pending, '\n')
			if i < 0 {
				break
			}
			line := strings.ToLower(l.pending[:i])
			l.pending = l.pending[i+1:]
			if strings.Contains(line, "timeout") || strings.Contains(line, "deadline exceeded") {
				l.status.Timeouts++
			}
			if strings.Contains(line, "connection refused") {
				l.status.Refused++
			}
			if strings.Contains(line, "tls") && (strings.Contains(line, "error") || strings.Contains(line, "failed")) {
				l.status.TLSFailures++
			}
			if strings.Contains(line, "network is unreachable") || strings.Contains(line, "no route to host") {
				l.status.NetworkErrors++
			}
		}
		if len(l.pending) > 4096 {
			l.pending = l.pending[len(l.pending)-4096:]
		}
		p = p[written:]
		if err != nil {
			return total, err
		}
		if written == 0 {
			return total, fmt.Errorf("short log write")
		}
	}
	return total, nil
}
func (l *rotatingLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
func (l *rotatingLog) Status() model.ProcessStatus { l.mu.Lock(); defer l.mu.Unlock(); return l.status }

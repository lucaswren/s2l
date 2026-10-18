package singbox

import (
	"fmt"
	"github.com/lucaswren/s2l/internal/debuglog"
	"os"
	"sync"

	"github.com/lucaswren/s2l/internal/model"
)

const logLimit int64 = 5 * 1024 * 1024

type rotatingLog struct {
	sessionOnly bool
	mu          sync.Mutex
	path        string
	file        *os.File
	size        int64
	limit       int64
	status      model.ProcessStatus
	classifier  lineClassifier
	closed      bool
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
	if l.closed {
		return 0, os.ErrClosed
	}
	if l.sessionOnly {
		debuglog.Default.Add("sing-box/"+l.path, p)
		l.observe(p)
		return len(p), nil
	}
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
		debuglog.Default.Add("sing-box/"+l.path, p[:written])
		l.size += int64(written)
		total += written
		l.observe(p[:written])
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
	l.closed = true
	l.classifier = lineClassifier{}
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
func (l *rotatingLog) Status() model.ProcessStatus { l.mu.Lock(); defer l.mu.Unlock(); return l.status }

// Classification retains only keyword-match positions and flags, never raw
// partial lines. Disabled Debug therefore keeps counters without retaining log
// fragments, and arbitrary stdout/stderr chunk boundaries don't lose errors.
func (l *rotatingLog) observe(p []byte) {
	l.status.LogBytes += uint64(len(p))
	for _, ch := range p {
		if ch == '\n' {
			f := l.classifier.found
			if f[0] || f[1] {
				l.status.Timeouts++
			}
			if f[2] {
				l.status.Refused++
			}
			if f[3] && (f[4] || f[5]) {
				l.status.TLSFailures++
			}
			if f[6] || f[7] {
				l.status.NetworkErrors++
			}
			if f[8] {
				l.status.ProxyRejections++
			}
			l.classifier = lineClassifier{}
			continue
		}
		if ch >= 'A' && ch <= 'Z' {
			ch += 'a' - 'A'
		}
		for i, word := range logKeywords {
			if l.classifier.found[i] {
				continue
			}
			j := l.classifier.matched[i]
			for j > 0 && word[j] != ch {
				j = logKeywordPrefixes[i][j-1]
			}
			if word[j] == ch {
				j++
			}
			if j == len(word) {
				l.classifier.found[i] = true
			}
			l.classifier.matched[i] = j
		}
	}
}

type lineClassifier struct {
	matched [9]int
	found   [9]bool
}

var logKeywords = [9]string{"timeout", "deadline exceeded", "connection refused", "tls", "error", "failed", "network is unreachable", "no route to host", "socks5: request rejected"}
var logKeywordPrefixes = func() (out [9][]int) {
	for k, word := range logKeywords {
		out[k] = make([]int, len(word))
		for i, j := 1, 0; i < len(word); i++ {
			for j > 0 && word[i] != word[j] {
				j = out[k][j-1]
			}
			if word[i] == word[j] {
				j++
			}
			out[k][i] = j
		}
	}
	return
}()

func openDebugLog(path string) (*rotatingLog, error) {
	return &rotatingLog{path: path, sessionOnly: true}, nil
}

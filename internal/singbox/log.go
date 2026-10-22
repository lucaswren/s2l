package singbox

import (
	"github.com/lucaswren/s2l/internal/debuglog"
	"os"
	"sync"

	"github.com/lucaswren/s2l/internal/model"
)

// Output is counted continuously and retained only during bounded Debug sessions.
type processLog struct {
	mu         sync.Mutex
	path       string
	status     model.ProcessStatus
	classifier lineClassifier
	closed     bool
}

func (l *processLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, os.ErrClosed
	}
	debuglog.Default.Add("sing-box/"+l.path, p)
	l.observe(p)
	return len(p), nil
}
func (l *processLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	l.classifier = lineClassifier{}
	return nil
}
func (l *processLog) Status() model.ProcessStatus { l.mu.Lock(); defer l.mu.Unlock(); return l.status }

// Classification retains only keyword-match positions and flags, never raw
// partial lines. Disabled Debug therefore keeps counters without retaining log
// fragments, and arbitrary stdout/stderr chunk boundaries don't lose errors.
func (l *processLog) observe(p []byte) {
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
			if f[9] {
				l.status.DNSParseErrors++
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
	matched [10]int
	found   [10]bool
}

var logKeywords = [10]string{"timeout", "deadline exceeded", "connection refused", "tls", "error", "failed", "network is unreachable", "no route to host", "socks5: request rejected", "unpack request: dns:"}
var logKeywordPrefixes = func() (out [10][]int) {
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

func openDebugLog(path string) (*processLog, error) {
	return &processLog{path: path}, nil
}

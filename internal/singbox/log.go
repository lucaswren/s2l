package singbox

import (
	"github.com/lucaswren/s2l/internal/debuglog"
	"os"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

// Output is counted continuously; a 32 KiB tail supports automatic fault
// evidence without enabling verbose logging or writing a steady disk stream.
type processLog struct {
	mu         sync.Mutex
	path       string
	status     model.ProcessStatus
	classifier lineClassifier
	closed     bool
	tail       []byte
	tailHead   int
}

func (l *processLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, os.ErrClosed
	}
	debuglog.Default.Add("sing-box/"+l.path, p)
	l.observe(p)
	l.appendTail(p)
	return len(p), nil
}
func (l *processLog) appendTail(p []byte) {
	const limit = 32 << 10
	if len(p) > limit {
		p = p[len(p)-limit:]
	}
	if len(l.tail) < limit {
		n := min(len(p), limit-len(l.tail))
		l.tail = append(l.tail, p[:n]...)
		p = p[n:]
	}
	if len(p) > 0 {
		n := copy(l.tail[l.tailHead:], p)
		copy(l.tail, p[n:])
		l.tailHead = (l.tailHead + len(p)) % limit
	}
}
func (l *processLog) Tail() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := make([]byte, len(l.tail))
	n := copy(b, l.tail[l.tailHead:])
	copy(b[n:], l.tail[:l.tailHead])
	return string(b)
}
func (l *processLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	l.classifier = lineClassifier{}
	return nil
}
func (l *processLog) Status() model.ProcessStatus { l.mu.Lock(); defer l.mu.Unlock(); return l.status }

// Classification is independent of the bounded log tail, so arbitrary
// stdout/stderr chunk boundaries do not lose errors.
func (l *processLog) observe(p []byte) {
	l.status.LogBytes += uint64(len(p))
	for _, ch := range p {
		if ch == '\n' {
			f := l.classifier.found
			if f[0] || f[1] || f[2] || (f[3] && (f[4] || f[5])) || f[6] || f[7] || f[8] || f[9] {
				l.status.ErrorLines++
				l.status.LastErrorAt = time.Now()
			}
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
	return &processLog{path: path, status: model.ProcessStatus{StartedAt: time.Now()}}, nil
}

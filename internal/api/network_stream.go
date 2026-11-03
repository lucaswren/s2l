package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

func mtrArguments(target, port, protocol string, stream bool) []string {
	args := []string{"--no-dns", "--report-cycles", strconv.Itoa(networkProbeAttempts), "--interval", "0.2", "--max-ttl", "20", "--gracetime", "2"}
	if stream {
		args = append(args, "--raw")
	} else {
		args = append(args, "--report", "--report-wide")
	}
	if protocol != "icmp" {
		args = append(args, "--tcp", "--port", port)
	}
	if ip := net.ParseIP(strings.Split(target, "%")[0]); ip != nil && ip.To4() != nil {
		args = append(args, "-4")
	} else {
		args = append(args, "-6")
	}
	return append(args, target)
}

type networkStream struct {
	mu     sync.Mutex
	w      http.ResponseWriter
	cancel context.CancelFunc
	failed bool
}

func (s *networkStream) send(kind string, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return
	}
	if err := json.NewEncoder(s.w).Encode(map[string]any{"type": kind, "data": data}); err != nil {
		s.failed = true
		s.cancel()
		return
	}
	if err := http.NewResponseController(s.w).Flush(); err != nil {
		s.failed = true
		s.cancel()
	}
}

type liveProbeOutput struct {
	mu      sync.Mutex
	capture *boundedOutput
	stream  *networkStream
	pending []byte
}

func (l *liveProbeOutput) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.capture.mu.Lock()
	remaining := 32768 - len(l.capture.data)
	l.capture.mu.Unlock()
	n, err := l.capture.Write(p)
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		data := append(l.pending, p[:remaining]...)
		end := 0
		for end < len(data) && utf8.FullRune(data[end:]) {
			_, size := utf8.DecodeRune(data[end:])
			end += size
		}
		if end > 0 {
			l.stream.send("output", string(data[:end]))
		}
		l.pending = append([]byte(nil), data[end:]...)
	}
	return n, err
}
func mtrReached(output, target string) bool {
	expected := net.ParseIP(strings.Split(target, "%")[0])
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "h" {
			if ip := net.ParseIP(strings.Split(fields[2], "%")[0]); ip != nil && ip.Equal(expected) {
				return true
			}
		}
	}
	return false
}

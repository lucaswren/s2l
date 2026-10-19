package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lucaswren/s2l/internal/speedtest"
)

type networkTestRequest struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}
type tcpSample struct {
	Milliseconds float64 `json:"milliseconds"`
	Error        string  `json:"error,omitempty"`
}

// boundedOutput drains command output without retaining unbounded child output.
type boundedOutput struct {
	mu    sync.Mutex
	data  []byte
	total uint64
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	b.total += uint64(n)
	remaining := 32768 - len(b.data)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
func validProbeTarget(target string) bool {
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" || len(host) > 253 {
		return false
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if strings.HasPrefix(host, "-") {
		return false
	}
	for _, c := range host {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}
func (s *Server) handleNodeNetworkTest(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	node, ok := s.store.GetNode(id)
	if !ok {
		writeError(w, 404, "node not found")
		return
	}
	var body networkTestRequest
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	if decodeJSON(r, &body) != nil {
		writeError(w, 400, "invalid json")
		return
	}
	if body.Kind != "ping" && body.Kind != "tcping" && body.Kind != "mtr" && body.Kind != "protocols" {
		writeError(w, 400, "kind must be protocols, ping, tcping or mtr")
		return
	}
	if body.Kind == "tcping" && !validProbeTarget(body.Target) {
		writeError(w, 400, "target must be host:port (IPv6: [address]:port)")
		return
	}
	select {
	case s.networkTests <- struct{}{}:
		defer func() { <-s.networkTests }()
	default:
		writeError(w, 429, "network tests busy; retry later")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	result := map[string]any{"started_at": time.Now().UTC(), "node_id": node.ID, "kind": body.Kind, "target": body.Target, "path": "S2L → SOCKS5 节点 → 目标（TCP 建连，包含代理握手耗时）"}
	if body.Kind == "protocols" {
		report := speedtest.HealthReport{}
		if s.svc != nil {
			report = s.svc.ProbeNode(ctx, node)
		} else {
			report = speedtest.ProbeHealth(ctx, node)
		}
		result["path"] = "S2L → SOCKS5 节点 → 探测目标（不经过客户端和 L2TP）"
		result["target"] = "HTTPS / UDP 中继 / DNS"
		result["protocols"] = map[string]any{"tcp": report.TCP, "udp": report.UDP, "dns": report.DNS}
		result["ok"] = report.TCP.State == "healthy" && report.UDP.State == "healthy" && report.DNS.State == "healthy"
	} else if body.Kind == "tcping" {
		samples := []tcpSample{}
		successes := 0
		for i := 0; i < 3 && ctx.Err() == nil; i++ {
			attempt, stop := context.WithTimeout(ctx, 6*time.Second)
			started := time.Now()
			conn, err := speedtest.DialSOCKS5(attempt, node.Addr, node.Username, node.Password, body.Target)
			elapsed := time.Since(started)
			stop()
			sample := tcpSample{Milliseconds: float64(elapsed.Microseconds()) / 1000}
			if err != nil {
				sample.Error = "SOCKS5 TCP 建连失败或超时"
			} else {
				successes++
				conn.Close()
			}
			samples = append(samples, sample)
		}
		result["samples"] = samples
		result["successes"] = successes
		result["attempts"] = len(samples)
		result["ok"] = successes == 3
	} else {
		result["path"] = "S2L 服务器 → 节点地址（不经过 SOCKS5 转发）"
		host, _, err := net.SplitHostPort(node.Addr)
		if err != nil {
			writeError(w, 400, "invalid node address")
			return
		}
		// Resolve once, then pass only a numeric address, never user-supplied flags.
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			writeError(w, 400, "cannot resolve node address")
			return
		}
		target := addresses[0].String()
		result["target"] = target
		if runtime.GOOS != "linux" {
			writeError(w, 501, "Ping/MTR require a Linux server")
			return
		}
		args := []string{"-n", "-c", "3", "-W", "2", "-w", "12", target}
		if body.Kind == "mtr" {
			args = []string{"--report", "--no-dns", "--report-cycles", "3", "--max-ttl", "16", target}
		}
		binary, err := exec.LookPath(body.Kind)
		if err != nil {
			writeError(w, 501, fmt.Sprintf("缺少 %s：请在服务器安装 iputils-ping / mtr-tiny", body.Kind))
			return
		}
		cmd := exec.CommandContext(ctx, binary, args...)
		configureProbeCommand(cmd)
		cmd.WaitDelay = time.Second
		output := &boundedOutput{}
		cmd.Stdout = output
		cmd.Stderr = output
		err = cmd.Run()
		output.mu.Lock()
		result["output"] = string(output.data)
		result["output_limit_bytes"] = 32768
		result["output_truncated"] = output.total > uint64(len(output.data))
		output.mu.Unlock()
		result["ok"] = err == nil
		if ctx.Err() != nil {
			result["error"] = "测试已取消或达到 25 秒时限，输出可能不完整"
		} else if err != nil {
			result["error"] = "测试失败；请检查输出、原始套接字权限及目标是否响应"
		}
	}
	result["finished_at"] = time.Now().UTC()
	// Explicit results are returned only to this caller, never persisted or logged.
	writeJSON(w, 200, result)
}

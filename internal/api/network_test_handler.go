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

	"github.com/lucaswren/s2l/internal/diagnostics"
	"github.com/lucaswren/s2l/internal/model"
	"github.com/lucaswren/s2l/internal/network"
	"github.com/lucaswren/s2l/internal/speedtest"
)

const networkProbeAttempts = 8

type networkTestRequest struct {
	MappingID        string `json:"mapping_id,omitempty"`
	CompareMappingID string `json:"compare_mapping_id,omitempty"`
	CompareNode      string `json:"compare_node,omitempty"`
	Kind             string `json:"kind"`
	Target           string `json:"target"`
	Stream           bool   `json:"stream"`
	MTRProtocol      string `json:"mtr_protocol"`
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
	if body.Kind != "google" && body.Kind != "ping" && body.Kind != "tcping" && body.Kind != "https" && body.Kind != "mtr" && body.Kind != "protocols" && body.Kind != "quality" {
		writeError(w, 400, "kind must be google, protocols, ping, tcping, https, quality or mtr")
		return
	}
	if body.MTRProtocol != "" && body.MTRProtocol != "tcp" && body.MTRProtocol != "icmp" {
		writeError(w, 400, "mtr_protocol must be tcp or icmp")
		return
	}
	if (body.Kind == "tcping" || body.Kind == "https" || body.Kind == "quality") && !validProbeTarget(body.Target) {
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
	ctx, cancel := context.WithTimeout(r.Context(), 55*time.Second)
	defer cancel()
	var stream *networkStream
	result := map[string]any{"started_at": time.Now().UTC(), "node_id": node.ID, "kind": body.Kind, "target": body.Target, "path": "S2L → SOCKS5 节点 → 目标（TCP 建连，包含代理握手耗时）"}
	if body.Kind == "google" {
		nodes := []model.SocksNode{node}
		if body.CompareNode != "" {
			other, exists := s.store.GetNode(body.CompareNode)
			if !exists || other.ID == node.ID {
				writeError(w, 400, "请选择不同且存在的对照节点")
				return
			}
			nodes = append(nodes, other)
		}
		selected := []model.TunnelMapping{}
		mappings := s.store.ListMappings()
		for i, n := range nodes {
			wanted := body.MappingID
			if i > 0 {
				wanted = body.CompareMappingID
			}
			var chosen *model.TunnelMapping
			for _, m := range mappings {
				if m.NodeID != n.ID || (wanted != "" && m.ID != wanted) {
					continue
				}
				if chosen == nil || (chosen.Status != model.StatusRunning && m.Status == model.StatusRunning) {
					copy := m
					chosen = &copy
				}
			}
			if wanted != "" && chosen == nil {
				writeError(w, 400, "mapping does not belong to selected node")
				return
			}
			if chosen != nil {
				selected = append(selected, *chosen)
			}
		}
		before := network.ReadRejectCounters(ctx, selected)
		reports := make([]diagnostics.Report, len(nodes))
		mappingReports := make([]map[string]any, len(selected))
		mappingSlots := make(chan struct{}, 2)
		var wg sync.WaitGroup
		for i, n := range nodes {
			wg.Add(1)
			go func(i int, n model.SocksNode) { defer wg.Done(); reports[i] = diagnostics.Google(ctx, n) }(i, n)
		}
		for i, m := range selected {
			wg.Add(1)
			go func(i int, m model.TunnelMapping) {
				defer wg.Done()
				mappingReports[i] = map[string]any{"state": "unavailable"}
				if s.svc == nil || m.Status != model.StatusRunning {
					return
				}
				select {
				case mappingSlots <- struct{}{}:
					defer func() { <-mappingSlots }()
				case <-ctx.Done():
					return
				}
				report, err := s.svc.ProbeGoogleMapping(ctx, m.ID, m.NodeID)
				if err != nil {
					return
				}
				state := "healthy"
				if !report.Healthy() {
					state = "unavailable"
				}
				mappingReports[i] = map[string]any{"state": state, "report": report}
			}(i, m)
		}
		wg.Wait()
		after := network.ReadRejectCounters(ctx, selected)
		policies := []map[string]any{}
		for i, m := range selected {
			for _, n := range nodes {
				if m.NodeID == n.ID {
					policies = append(policies, map[string]any{"mapping_id": m.ID, "node_id": m.NodeID, "udp_policy": m.EffectiveUDPPolicy(), "dns_mode": m.EffectiveDNSMode(), "status": m.Status, "max_connections": m.MaxConnections, "max_sessions": m.EffectiveMaxSessions(), "upload_kbps": m.UploadKbps, "download_kbps": m.DownloadKbps, "reject_before": before[m.ID], "reject_after": after[m.ID]})
					policies[len(policies)-1]["mapping_dns"] = mappingReports[i]
				}
			}
		}
		result["schema_version"] = 1
		result["google"] = reports
		result["mapping_policies"] = policies
		result["ok"] = ctx.Err() == nil
		for _, report := range mappingReports {
			if report["state"] != "healthy" {
				result["ok"] = false
			}
		}
		result["client_path"] = "unverified"
		for _, report := range reports {
			if !report.Healthy() {
				result["ok"] = false
			}
		}
		result["path"] = "服务器 → 固定 SOCKS5 → 目标服务"
	} else if body.Kind == "protocols" {
		report := speedtest.HealthReport{}
		if s.svc != nil {
			report = s.svc.ProbeNode(ctx, node)
		} else {
			report = speedtest.ProbeHealth(ctx, node)
		}
		result["path"] = "S2L → SOCKS5 节点 → 探测目标"
		result["target"] = "HTTPS / UDP 中继 / DNS"
		result["capability_warning"] = report.UDP.State != "healthy"
		result["protocols"] = map[string]any{"tcp": report.TCP, "udp": report.UDP, "dns": report.DNS}
		result["ok"] = report.TCP.State == "healthy" && (report.DNS.State == "healthy" || report.DNS.State == "degraded")
	} else if body.Kind == "https" {
		measured := speedtest.ProbeHTTPS(ctx, node, body.Target)
		result["protocols"] = map[string]any{"tcp": measured}
		result["path"] = "S2L → SOCKS5 节点 → 指定 HTTPS 目标"
		result["ok"] = measured.State == "healthy"
	} else if body.Kind == "quality" {
		q := speedtest.ProbeQuality(ctx, node, body.Target)
		result["quality"] = q
		result["ok"] = q.Successes == networkProbeAttempts
		result["path"] = "S2L → 固定 SOCKS5 → HTTPS 响应传输"
	} else if body.Kind == "tcping" {
		samples := []tcpSample{}
		successes := 0
		for i := 0; i < networkProbeAttempts && ctx.Err() == nil; i++ {
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
		result["ok"] = successes == networkProbeAttempts
	} else {
		result["path"] = "S2L 服务器 → 节点地址（不经过 SOCKS5 转发）"
		host, port, err := net.SplitHostPort(node.Addr)
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
		for _, address := range addresses {
			if address.IP.To4() != nil {
				target = address.String()
				break
			}
		}
		result["target"] = target
		if runtime.GOOS != "linux" {
			writeError(w, 501, "Ping/MTR require a Linux server")
			return
		}
		args := []string{"-n", "-c", strconv.Itoa(networkProbeAttempts), "-W", "2", "-w", "25", target}
		if body.Kind == "mtr" {
			args = mtrArguments(target, port, body.MTRProtocol, body.Stream)
			result["mtr_protocol"] = "tcp"
			if body.MTRProtocol == "icmp" {
				result["mtr_protocol"] = "icmp"
			}
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
		if body.Kind == "mtr" && body.Stream {
			stream = &networkStream{w: w, cancel: cancel}
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.Header().Set("X-Accel-Buffering", "no")
			stream.send("start", result)
			live := &liveProbeOutput{capture: output, stream: stream}
			cmd.Stdout = live
			cmd.Stderr = live
		}
		err = cmd.Run()
		output.mu.Lock()
		result["output"] = string(output.data)
		result["output_limit_bytes"] = 32768
		result["output_truncated"] = output.total > uint64(len(output.data))
		output.mu.Unlock()
		result["ok"] = err == nil
		if body.Kind == "mtr" && body.Stream {
			result["reached"] = mtrReached(fmt.Sprint(result["output"]), target)
		}
		if ctx.Err() != nil {
			result["error"] = "测试已取消或达到 55 秒时限，输出可能不完整"
		} else if err != nil {
			result["error"] = "测试执行失败，请检查输出及服务器探测权限"
		}
	}
	result["finished_at"] = time.Now().UTC()
	// Explicit results are returned only to this caller, never persisted or logged.
	if stream != nil {
		stream.send("result", result)
		return
	}
	writeJSON(w, 200, result)
}

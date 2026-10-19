package debuglog

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"time"

	"github.com/lucaswren/s2l/internal/model"
)

const eventLimit = 512

type Event struct {
	At        time.Time `json:"at"`
	MappingID string    `json:"mapping_id,omitempty"`
	Kind      string    `json:"kind"`
	Detail    string    `json:"detail"`
}
type Resources struct {
	HeapBytes     uint64 `json:"heap_bytes"`
	Goroutines    int    `json:"goroutines"`
	RSSBytes      uint64 `json:"rss_bytes,omitempty"`
	OpenFDs       int    `json:"open_fds,omitempty"`
	DiskFreeBytes uint64 `json:"disk_free_bytes,omitempty"`
	DiskSampled   bool   `json:"disk_sampled"`
	Error         string `json:"error,omitempty"`
}
type Sample struct {
	At        time.Time `json:"at"`
	Resources Resources `json:"resources"`
}
type Analysis struct {
	Connections        []Connection   `json:"connections"`
	FailedTargets      []FailedTarget `json:"failed_targets"`
	DroppedConnections int            `json:"dropped_connections"`
	Events             []Event        `json:"events"`
	Resources          []Sample       `json:"resources"`
	Findings           []string       `json:"findings"`
	DroppedEvents      int            `json:"dropped_events"`
	Notice             string         `json:"notice"`
}
type observation struct {
	Health  model.UpstreamHealth
	Traffic model.TunTraffic
	PID     int
}

func SampleResources(dataDir string) Resources {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	r := Resources{HeapBytes: m.HeapAlloc, Goroutines: runtime.NumGoroutine()}
	platformResources(&r, dataDir)
	return r
}

func (c *Collector) event(e Event) {
	if len(c.events) >= eventLimit {
		copy(c.events, c.events[1:])
		c.events = c.events[:eventLimit-1]
		c.droppedEvents++
	}
	c.events = append(c.events, e)
}

func counterDelta(current, previous uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	return 0
}

// RecordStatus stores structured evidence only during an active debug session.
// Counter decreases establish a new baseline rather than producing underflow.
func (c *Collector) RecordStatus(status model.SystemStatus, resources Resources, now time.Time) {
	c.RecordSession(c.Session(), status, resources, now)
}

func (c *Collector) RecordSession(session uint64, status model.SystemStatus, resources Resources, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if session == 0 || session != c.generation || !c.active() || (!c.lastSample.IsZero() && !now.After(c.lastSample)) {
		return
	}
	if !c.lastSample.IsZero() {
		shift := now.Round(0).Sub(c.lastSample.Round(0)) - now.Sub(c.lastSample)
		if shift > 2*time.Second || shift < -2*time.Second {
			c.event(Event{At: now, Kind: "clock-adjustment", Detail: fmt.Sprintf("系统墙上时间相对单调时钟调整 %s；请结合采样顺序分析日志。", shift.Round(time.Millisecond))})
		}
	}
	c.lastSample = now
	// Raw and structured health evidence share one session and one commit.
	status.Mappings = append([]model.TunnelMapping(nil), status.Mappings...)
	for i := range status.Mappings {
		status.Mappings[i].L2TPPass = ""
	}
	if b, err := json.Marshal(status); err == nil {
		c.addLocked("health", b)
	}
	if c.previous == nil {
		c.previous = map[string]observation{}
	}
	traffic := map[string]model.TunTraffic{}
	for _, t := range status.TunTraffic {
		traffic[t.TunName] = t
	}
	next := map[string]observation{}
	for id, h := range status.UpstreamHealth {
		t, trafficExists := traffic[h.TunName]
		if !trafficExists {
			t.Error = "interface statistics unavailable"
		}
		old, exists := c.previous[id]
		event := func(kind, detail string) { c.event(Event{now, id, kind, detail}) }
		if !exists {
			event("baseline", fmt.Sprintf("首次采样：链路 %s，本地 %s，DNS %s；累计计数不作为本次新增故障", h.State, h.LocalState, h.Resolver.Active))
		} else {
			for _, protocol := range []struct {
				name              string
				current, previous model.ProtocolHealth
			}{
				{"tcp", h.TCP, old.Health.TCP}, {"udp", h.UDP, old.Health.UDP}, {"dns", h.DNS, old.Health.DNS},
			} {
				if protocol.current.State != protocol.previous.State || protocol.current.Method != protocol.previous.Method || protocol.current.Error != protocol.previous.Error {
					event("protocol-"+protocol.name, fmt.Sprintf("%s：%s → %s；%s；%s；检测时间 %s", protocol.name, protocol.previous.State, protocol.current.State, protocol.current.Method, protocol.current.Error, protocol.current.CheckedAt.UTC().Format(time.RFC3339)))
				}
			}
			if h.State != old.Health.State {
				event("health", old.Health.State+" → "+h.State)
			}
			if h.LocalState != old.Health.LocalState {
				event("local", old.Health.LocalState+" → "+h.LocalState)
			}
			if h.Recovery.Circuit != old.Health.Recovery.Circuit {
				event("recovery", old.Health.Recovery.Circuit+" → "+h.Recovery.Circuit)
			}
			if h.Resolver.Active != old.Health.Resolver.Active {
				event("dns-switch", old.Health.Resolver.Active+" → "+h.Resolver.Active)
			}
			reset := h.TunName != old.Health.TunName || status.SingboxPIDs[h.TunName] != old.PID || h.Process.Exits != old.Health.Process.Exits || h.Process.LogBytes < old.Health.Process.LogBytes
			if n := counterDelta(h.Process.Exits, old.Health.Process.Exits); n > 0 {
				event("process-exit", fmt.Sprintf("退出计数增加 %d", n))
			}
			// A new process owns fresh resolver/error counters; don't attribute its
			// lifetime totals to the previous process's sampling interval.
			if !reset {
				if n := counterDelta(h.Process.Timeouts, old.Health.Process.Timeouts); n > 0 {
					event("timeout", fmt.Sprintf("超时日志增加 %d", n))
				}
				if n := counterDelta(h.Process.TLSFailures, old.Health.Process.TLSFailures); n > 0 {
					event("tls", fmt.Sprintf("TLS 错误日志增加 %d", n))
				}
				if n := counterDelta(h.Process.Refused, old.Health.Process.Refused); n > 0 {
					event("refused", fmt.Sprintf("连接拒绝日志增加 %d", n))
				}
				if n := counterDelta(h.Process.DNSParseErrors, old.Health.Process.DNSParseErrors); n > 0 {
					event("dns-parse", fmt.Sprintf("DNS 请求解析错误增加 %d", n))
				}
				if n := counterDelta(h.Process.ProxyRejections, old.Health.Process.ProxyRejections); n > 0 {
					event("proxy-rejected", fmt.Sprintf("SOCKS5 请求拒绝日志增加 %d", n))
				}
				if n := counterDelta(h.Process.NetworkErrors, old.Health.Process.NetworkErrors); n > 0 {
					event("network", fmt.Sprintf("网络不可达日志增加 %d", n))
				}
				if n := counterDelta(h.Resolver.Failures, old.Health.Resolver.Failures); n > 0 {
					event("dns-failure", fmt.Sprintf("解析失败增加 %d", n))
				}
				if n := counterDelta(h.Resolver.Fallbacks, old.Health.Resolver.Fallbacks); n > 0 {
					event("dns-fallback", fmt.Sprintf("DNS 备用成功增加 %d", n))
				}
			}
			if !reset && t.Error == "" && old.Traffic.Error == "" {
				n := counterDelta(t.RxDropped, old.Traffic.RxDropped) + counterDelta(t.TxDropped, old.Traffic.TxDropped)
				if n > 0 {
					event("drops", fmt.Sprintf("接口丢包增加 %d", n))
				}
				n = counterDelta(t.RxErrors, old.Traffic.RxErrors) + counterDelta(t.TxErrors, old.Traffic.TxErrors)
				if n > 0 {
					event("interface-error", fmt.Sprintf("接口错误增加 %d", n))
				}
			}
		}
		next[id] = observation{Health: h, Traffic: t, PID: status.SingboxPIDs[h.TunName]}
	}
	for id := range c.previous {
		if _, ok := next[id]; !ok {
			c.event(Event{now, id, "removed", "映射已离开活动监控"})
		}
	}
	c.previous = next
	if len(c.samples) >= 360 {
		copy(c.samples, c.samples[1:])
		c.samples = c.samples[:359]
	}
	c.samples = append(c.samples, Sample{now, resources})
}

func (c *Collector) Analysis() Analysis {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.analysisLocked()
}
func (c *Collector) analysisLocked() Analysis {
	a := Analysis{Events: append([]Event{}, c.events...), Resources: append([]Sample{}, c.samples...), Findings: []string{}, DroppedEvents: c.droppedEvents, Notice: "基于 Debug 采样记录；5 秒内的短暂变化可能未被采样。"}
	a.Connections, a.FailedTargets = c.connectionSummaryLocked()
	a.DroppedConnections = c.droppedConnections
	sort.SliceStable(a.Events, func(i, j int) bool {
		if a.Events[i].At.Equal(a.Events[j].At) {
			return a.Events[i].MappingID < a.Events[j].MappingID
		}
		return a.Events[i].At.Before(a.Events[j].At)
	})
	ids := []string{}
	for id := range c.previous {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		h := c.previous[id].Health
		if h.LocalState != "" && h.LocalState != "healthy" {
			a.Findings = append(a.Findings, fmt.Sprintf("%s：最近采样本地链路为 %s，应先检查本地进程和恢复状态。", id, h.LocalState))
		}
		if h.TCP.State == "healthy" && (h.UDP.State == "unsupported" || h.UDP.State == "unavailable" || h.UDP.State == "unstable") {
			a.Findings = append(a.Findings, fmt.Sprintf("%s：TCP 正常但 UDP 为 %s；请核对拒绝码和节点 UDP 支持情况。", id, h.UDP.State))
		}
		if h.TCP.State == "healthy" && (h.DNS.State == "unavailable" || h.DNS.State == "degraded") {
			a.Findings = append(a.Findings, fmt.Sprintf("%s：TCP 正常而 DNS 异常或仅备用可用，建议先核对 DNS 时间线。", id))
		}
	}
	if len(a.Resources) > 0 {
		a.Notice += fmt.Sprintf(" 采样窗口：%s 至 %s；导出时的实时状态可能属于更晚时间。", a.Resources[0].At.UTC().Format(time.RFC3339), a.Resources[len(a.Resources)-1].At.UTC().Format(time.RFC3339))
		last := a.Resources[len(a.Resources)-1].Resources
		if last.DiskSampled && last.DiskFreeBytes < 256<<20 {
			a.Findings = append(a.Findings, "数据目录所在磁盘可用空间低于 256 MiB，请及时检查容量，避免配置写入或系统服务失败。")
		}
	}
	kinds := map[string]bool{}
	for _, e := range a.Events {
		kinds[e.Kind] = true
	}
	if kinds["process-exit"] || kinds["local"] {
		a.Findings = append(a.Findings, "观察到进程退出或本地链路变化，优先核对恢复事件及系统资源趋势。")
	}
	if kinds["dns-failure"] || kinds["dns-fallback"] || kinds["dns-switch"] {
		a.Findings = append(a.Findings, "观察到 DNS 失败、备用解析或切换，需对照相同时间段的连接错误。")
	}
	if kinds["drops"] || kinds["interface-error"] {
		a.Findings = append(a.Findings, "观察到接口丢包或错误增加，需结合吞吐、队列和客户端路径进一步核实。")
	}
	if kinds["clock-adjustment"] {
		a.Findings = append(a.Findings, "采样期间检测到系统时间校正，绝对时间可能跳变；Debug 到期使用单调时钟，不受 NTP 校时影响。")
	}
	if kinds["dns-parse"] {
		a.Findings = append(a.Findings, "观察到 DNS 请求报文解析失败；上游 DNS 探测正常也不能排除客户端请求异常，需核对原始报文，不能据此判断 MTU 故障。")
	}
	if kinds["proxy-rejected"] {
		a.Findings = append(a.Findings, "观察到 SOCKS5 请求被节点拒绝，请核对拒绝码、UDP 支持及节点访问策略。")
	}
	if kinds["timeout"] || kinds["tls"] || kinds["refused"] || kinds["network"] {
		a.Findings = append(a.Findings, "观察到连接错误日志增量；单凭此计数不能确定目标、上游或客户端的责任。")
	}
	if len(a.Findings) == 0 {
		a.Findings = append(a.Findings, "暂无足够的故障变化证据；请开启 Debug 后复现问题，再导出分析。")
	}
	return a
}

func (a Analysis) Text() []byte {
	b := []byte(a.Notice + "\n\n")
	for _, f := range a.Findings {
		b = append(b, []byte("- "+f+"\n")...)
	}
	for _, target := range a.FailedTargets {
		b = append(b, []byte(fmt.Sprintf("目标错误观察 %s %s：%d 条连接（最近保留记录）\n", target.Protocol, target.Target, target.Count))...)
	}
	for _, e := range a.Events {
		b = append(b, []byte(fmt.Sprintf("%s [%s] %s: %s\n", e.At.UTC().Format(time.RFC3339), e.MappingID, e.Kind, e.Detail))...)
	}
	return b
}

func (a Analysis) JSON() []byte { b, _ := json.MarshalIndent(a, "", "  "); return b }

package model

import "time"

// 映射运行状态
const (
	StatusRunning = "running"
	StatusStopped = "stopped"
	StatusError   = "error"
)

// SocksNode 上游 SOCKS5 代理节点
type SocksNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Addr     string `json:"addr"` // host:port
	Username string `json:"username"`
	Password string `json:"password"`
}

// TunnelMapping 一条「SOCKS5 → TUN → 路由表 → L2TP」完整映射
type TunnelMapping struct {
	ID          string `json:"id"`
	NodeID      string `json:"node_id"`
	TunName     string `json:"tun_name"`  // 如 tun101
	TableID     int    `json:"table_id"`  // 如 101
	Subnet      string `json:"subnet"`    // L2TP 专属子网，如 10.0.101.0/24
	L2TPUser    string `json:"l2tp_user"` // L2TP 拨号账号
	L2TPPass    string `json:"l2tp_pass"` // L2TP 拨号密码
	Status      string `json:"status"`    // running / stopped / error
	ErrorMsg    string `json:"error_msg,omitempty"`
	MTU         int    `json:"mtu,omitempty"`
	MSS         int    `json:"mss,omitempty"`
	QueueLength int    `json:"queue_length,omitempty"`
	UDPPolicy   string `json:"udp_policy,omitempty"` // reject-quic (default) | allow | reject
	DNSMode     string `json:"dns_mode,omitempty"`   // auto (default) | doh | tcp
}

func (m TunnelMapping) EffectiveMTU() int {
	if m.MTU == 0 {
		return 1400
	}
	return m.MTU
}
func (m TunnelMapping) EffectiveMSS() int {
	if m.MSS == 0 {
		return 1200
	}
	return m.MSS
}
func (m TunnelMapping) EffectiveQueueLength() int {
	if m.QueueLength == 0 {
		return 1000
	}
	return m.QueueLength
}
func (m TunnelMapping) EffectiveUDPPolicy() string {
	if m.UDPPolicy == "" {
		return "reject-quic"
	}
	return m.UDPPolicy
}
func (m TunnelMapping) EffectiveDNSMode() string {
	if m.DNSMode == "" {
		return "auto"
	}
	return m.DNSMode
}

type ResolverStatus struct {
	Mode      string `json:"mode"`
	Active    string `json:"active"`
	Queries   uint64 `json:"queries"`
	Failures  uint64 `json:"failures"`
	Fallbacks uint64 `json:"fallbacks"`
	Switches  uint64 `json:"switches"`
}

type ProcessStatus struct {
	ProxyRejections uint64 `json:"proxy_rejections"`
	LogBytes        uint64 `json:"log_bytes"`
	Timeouts        uint64 `json:"timeouts"`
	Refused         uint64 `json:"refused"`
	TLSFailures     uint64 `json:"tls_failures"`
	NetworkErrors   uint64 `json:"network_errors"`
	Exits           uint64 `json:"exits"`
}

// TunTraffic 单个 TUN 网卡流量快照（供 /api/status 使用）
type TunTraffic struct {
	TunName   string `json:"tun_name"`
	RxBytes   uint64 `json:"rx_bytes"`
	TxBytes   uint64 `json:"tx_bytes"`
	RxPackets uint64 `json:"rx_packets"`
	TxPackets uint64 `json:"tx_packets"`
	RxDropped uint64 `json:"rx_dropped"`
	TxDropped uint64 `json:"tx_dropped"`
	RxErrors  uint64 `json:"rx_errors"`
	TxErrors  uint64 `json:"tx_errors"`
	Error     string `json:"error,omitempty"`
}

// ProtocolHealth describes one separately verified upstream capability.
type ProtocolHealth struct {
	State     string    `json:"state"`
	Method    string    `json:"method,omitempty"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

type RecoveryStatus struct {
	Circuit      string    `json:"circuit"`
	Attempts     int       `json:"attempts"`
	Failures     int       `json:"failures"`
	NextRetry    time.Time `json:"next_retry"`
	HealthySince time.Time `json:"healthy_since"`
}

type UpstreamHealth struct {
	Recovery       RecoveryStatus `json:"recovery"`
	TCP            ProtocolHealth `json:"tcp"`
	UDP            ProtocolHealth `json:"udp"`
	DNS            ProtocolHealth `json:"dns"`
	Resolver       ResolverStatus `json:"resolver"`
	Process        ProcessStatus  `json:"process"`
	LocalState     string         `json:"local_state"`
	LocalCheckedAt time.Time      `json:"local_checked_at"`
	RecoveryError  string         `json:"recovery_error,omitempty"`
	State          string         `json:"state"`
	TunName        string         `json:"tun_name"`
	Error          string         `json:"error,omitempty"`
	CheckedAt      time.Time      `json:"checked_at"`
	OutageSince    time.Time      `json:"outage_since"`
	RecoveredAt    time.Time      `json:"recovered_at"`
}

type SystemStatus struct {
	UpstreamHealth map[string]UpstreamHealth `json:"upstream_health"`
	Uptime         time.Duration             `json:"uptime_ns"`
	Mappings       []TunnelMapping           `json:"mappings"`
	TunTraffic     []TunTraffic              `json:"tun_traffic"`
	SingboxPIDs    map[string]int            `json:"singbox_pids"` // tun_name -> pid
}

// AppState 持久化到 data/state.json 的完整状态
type AppState struct {
	Nodes    []SocksNode     `json:"nodes"`
	Mappings []TunnelMapping `json:"mappings"`
}

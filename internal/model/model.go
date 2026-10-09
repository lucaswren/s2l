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
	ID       string `json:"id"`
	NodeID   string `json:"node_id"`
	TunName  string `json:"tun_name"`  // 如 tun101
	TableID  int    `json:"table_id"`  // 如 101
	Subnet   string `json:"subnet"`    // L2TP 专属子网，如 10.0.101.0/24
	L2TPUser string `json:"l2tp_user"` // L2TP 拨号账号
	L2TPPass string `json:"l2tp_pass"` // L2TP 拨号密码
	Status   string `json:"status"`    // running / stopped / error
	ErrorMsg string `json:"error_msg,omitempty"`
}

// TunTraffic 单个 TUN 网卡流量快照（供 /api/status 使用）
type TunTraffic struct {
	TunName   string `json:"tun_name"`
	RxBytes   uint64 `json:"rx_bytes"`
	TxBytes   uint64 `json:"tx_bytes"`
	RxPackets uint64 `json:"rx_packets"`
	TxPackets uint64 `json:"tx_packets"`
}

// SystemStatus 系统运行状态汇总
type SystemStatus struct {
	Uptime      time.Duration        `json:"uptime_ns"`
	Mappings    []TunnelMapping      `json:"mappings"`
	TunTraffic  []TunTraffic         `json:"tun_traffic"`
	SingboxPIDs map[string]int       `json:"singbox_pids"` // tun_name -> pid
}

// AppState 持久化到 data/state.json 的完整状态
type AppState struct {
	Nodes    []SocksNode     `json:"nodes"`
	Mappings []TunnelMapping `json:"mappings"`
}

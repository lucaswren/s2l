package buildinfo

import (
	"regexp"
	"runtime/debug"
)

// Populated by the managed installation and upgrade scripts.
var Version = "development"
var Revision = ""

type Info struct {
	Version      string            `json:"version"`
	Revision     string            `json:"revision"`
	Modified     bool              `json:"modified"`
	ReleaseNotes map[string]string `json:"release_notes,omitempty"`
	ReleaseURL   string            `json:"release_url,omitempty"`
}

func Current() Info {
	result := Info{Version: Version, Revision: Revision}
	if data, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range data.Settings {
			if setting.Key == "vcs.revision" && result.Revision == "" {
				result.Revision = setting.Value
			}
			if setting.Key == "vcs.modified" {
				result.Modified = setting.Value == "true"
			}
		}
	}
	result.ReleaseNotes = map[string]string{
		"en":    "Automatically retain bounded fault status history, process logs and short IPv4 header-only captures, with queued collection across mappings and ZIP export after service restarts. Detect conflicting routes and verify actual PPP outbound and return paths without deleting foreign configuration. Preserve fixed SOCKS5 exits and per-mapping bandwidth limits. Keep common network tests visible and move specialist tests under Advanced tests. Missing tcpdump retains snapshots and logs; client-side iWAN faults and unavailable upstream nodes still require investigation. Upgrades restart mappings and require clients to reconnect.",
		"zh-TW": "新增故障自動取證，保存有限的狀態歷史、程序日誌與短時 IPv4 報文頭抓包，支援多映射排隊及服務重啟後匯出 ZIP。加強路由衝突檢查，驗證實際 PPP 出口與回程，不自動刪除外部設定。保留固定 SOCKS5 出口及每映射頻寬限制；常用網路測試直接顯示，專項測試收納至進階檢測。缺少 tcpdump 時仍保存快照與日誌；客戶端 iWAN 或上游節點故障仍需定位。升級會重建映射，客戶端須重新連線。",
		"zh-CN": "新增故障自动取证，保存有限的状态历史、进程日志与短时 IPv4 报文头抓包，支持多映射排队及服务重启后导出 ZIP。加强路由冲突检查，验证实际 PPP 出口与回程，不自动删除外部配置。保留固定 SOCKS5 出口及每映射带宽限制；常用网络测试直接显示，专项测试收纳至高级检测。缺少 tcpdump 时仍保存快照与日志；客户端 iWAN 或上游节点故障仍需定位。升级会重建映射，客户端须重新连接。",
	}
	if regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

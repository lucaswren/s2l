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
		"en":    "Manage mapping network settings automatically while preserving per-mapping upload/download bandwidth limits, credentials, groups and connection observation. Retire TCP connection caps and manual MTU/MSS/queue, UDP and DNS controls; back up and migrate legacy configurations, normalize old-backup restores, and reclaim marked legacy firewall limits. Automatic DNS immediately retries errors on the alternate transport and races it after 300 ms without an answer, including TCP-to-DoH fallback; cancel losing requests and preserve fixed SOCKS5 exits. Upgrades restart mappings and require clients to reconnect. This does not repair an unavailable upstream node.",
		"zh-TW": "統一管理映射網路參數，保留每映射上傳／下載頻寬、憑據、分組及連線觀測。取消 TCP 連線數上限及手動 MTU／MSS／佇列、UDP、DNS 設定；先備份再遷移舊設定，恢復舊備份也統一參數，清理有歸屬標記的舊防火牆限制。自動 DNS 失敗立即嘗試另一方式，超過 300 毫秒未回覆則並行嘗試備用，支援 TCP 回退 DoH；取消較慢請求並維持固定 SOCKS5 出口。升級會重建映射，客戶端須重新連線；無法修復上游節點整體不可用。",
		"zh-CN": "统一管理映射网络参数，保留每映射上传／下载带宽、凭据、分组及连接观测。取消 TCP 连接数上限及手动 MTU／MSS／队列、UDP、DNS 设置；先备份再迁移旧配置，恢复旧备份也统一参数，清理有归属标记的旧防火墙限制。自动 DNS 失败立即尝试另一方式，超过 300 毫秒未响应则并发尝试备用，支持 TCP 回退 DoH；取消较慢请求并保持固定 SOCKS5 出口。升级会重建映射，客户端须重新连接；无法修复上游节点整体不可用。",
	}
	if regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

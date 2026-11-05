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
		"en":    "1.0.3: Dedicated Monitoring tab with client upload/download rates, new versus cumulative classified errors, last-error time and stale-snapshot warnings. Preserve host statistics and collection timestamps; bound refresh requests and show UDP rejection as policy rather than a pending probe. Refresh multilingual documentation and remove the old monitoring disclosure. Upgrade during a maintenance window: mappings restart and clients must reconnect.",
		"zh-TW": "1.0.3：運行監控獨立分頁，顯示用戶端上下行速率、新增與累計分類錯誤、最近錯誤時間及過期快照提示。修復全機統計與採集時間遺失，為狀態更新設定逾時，按策略拒絕 UDP 不再顯示為等待檢測。更新三語文件並移除舊監控摺疊入口。請於維護時段升級：映射會重啟，用戶端須重新連線。",
		"zh-CN": "1.0.3：运行监控独立标签页，显示客户端上下行速率、新增与累计分类错误、最近错误时间及过期快照提示。修复全机统计与采集时间丢失，为状态刷新设置超时，按策略拒绝 UDP 不再显示为等待检测。更新三语文档并移除旧监控折叠入口。请在维护窗口升级：映射会重启，客户端须重新连接。",
	}
	if regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

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
		"en":    "1.0.4: Keep fast DNS fallback while requiring confirmed primary failures for switching. Export PPP traffic deltas and correlate disconnects with daemon logs. Show DNS and health-probe SOCKS5 replies separately from unclassified forwarding errors. Upgrade during a maintenance window because mappings restart.",
		"zh-TW": "1.0.4：保留快速 DNS 備援，確認主通道故障後再切換。取證新增 PPP 流量增量與斷線日誌關聯。分開顯示 DNS、健康探測和未分類的轉發代理拒絕。升級會重啟映射，請安排維護時段。",
		"zh-CN": "1.0.4：保留快速 DNS 兜底，确认主通道故障后再切换。取证新增 PPP 流量增量与断线日志关联。分开显示 DNS、健康探测和未归类的转发代理拒绝。升级会重启映射，请安排维护窗口。",
	}
	if regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

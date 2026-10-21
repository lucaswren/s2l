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
		"zh-CN": "Google HTTPS DNS 记录原样转发，增加映射实际 DNS 路径检测；统一监控折叠、表单和按钮布局，登出移至顶部；增加配置备份清理、升级阶段记录及版本信息。固定出口、DoH/TCP 和 UDP 主动拒绝保持不变。",
		"zh-TW": "Google HTTPS DNS 記錄原樣轉送，增加映射實際 DNS 路徑檢測；統一監控收合、表單與按鈕版面，登出移至頂端；增加設定備份清理、升級階段記錄與版本資訊。固定出口、DoH/TCP 與 UDP 主動拒絕保持不變。",
		"en":    "Preserve Google HTTPS DNS records and test the live mapping DNS path. Unify monitoring disclosures, forms and buttons; move sign-out to the header. Add backup cleanup, upgrade phase history and build information. Fixed exits, DoH/TCP and active UDP rejection are unchanged.",
	}
	if regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

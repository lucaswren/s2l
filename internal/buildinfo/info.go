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
		"en":    "Support complete uninstall after upgrading older S2L installations without dependency receipts. Reconstruct candidates from managed configuration and original package metadata, then recheck shared usage at removal. Remove exclusive VPN tools and recognized sing-box while preserving shared configurations, services and reverse dependencies; a shared component no longer keeps unrelated dedicated packages. Existing ownership records are preserved. Validate real v1.3.12 upgrades, shared dependency retention, full removal and subsequent dialing on Ubuntu 24.04 and Debian 12.",
		"zh-TW": "支援舊版 S2L 升級後完整解除安裝：缺少相依套件記錄時，依受管設定及系統套件原始資料補建待核驗清單，解除安裝時重新檢查共用情況。清除獨占 VPN 工具與已辨識的 sing-box，保留共用設定、服務及反向相依元件；共用元件不再導致其他獨占套件一併保留。已有歸屬記錄不覆寫。以真實 v1.3.12 在 Ubuntu 24.04、Debian 12 驗證升級、共用元件保留、完整移除及後續撥號。",
		"zh-CN": "支持旧版 S2L 升级后完整卸载：缺少依赖记录时，依据受管配置和系统包原始数据补建待核验清单，卸载时重新检查共享情况。清除独占 VPN 工具和已识别的 sing-box，保留共享配置、服务及反向依赖组件；共享组件不再导致其他独占软件包一起保留。已有归属记录不覆盖。以真实 v1.3.12 在 Ubuntu 24.04、Debian 12 验证升级、共享组件保留、完整移除和后续拨号。",
	}
	if regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

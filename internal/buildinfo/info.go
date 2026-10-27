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
		"en":    "Add s2l uninstall (menu 13) with read-only preview and UNINSTALL confirmation. Remove managed data, credentials, certificates, services, network rules, runtime caches, clean default source and recognized S2L backups, then verify no managed resources remain. Fresh installs record dedicated VPN dependency ownership; remove only unchanged, unshared recorded dependencies. Preserve SSH, shared tools/services, kernels and system journal. Failed cleanup retains recovery data and may leave services stopped. Uninstall disconnects VPN clients; prompts follow the panel language.",
		"zh-TW": "新增 s2l uninstall（選單 13），支援只讀預覽及 UNINSTALL 確認。清除受管資料、帳密、憑證、服務、網路規則、執行快取、乾淨的預設原始碼與已辨識的 S2L 備份，並核對殘留。新安裝記錄專用 VPN 相依套件歸屬，僅移除未修改且未共用的已記錄項目。保留 SSH、共用工具／服務、核心與系統日誌。失敗保留復原資料，服務可能維持停止。解除安裝會中斷 VPN；提示沿用面板語言。",
		"zh-CN": "新增 s2l uninstall（菜单 13），支持只读预览及 UNINSTALL 确认。清除受管数据、账号密码、证书、服务、网络规则、运行缓存、干净的默认源码和已识别的 S2L 备份，并核对残留。新安装记录专用 VPN 依赖归属，仅移除未修改且未共享的已记录项目。保留 SSH、共享工具／服务、内核和系统日志。失败保留恢复数据，服务可能保持停止。卸载会中断 VPN；提示沿用面板语言。",
	}
	if regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

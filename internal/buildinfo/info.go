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
		"en":    "1.0.5: Repair PPP admission and firewall drift, revoke access immediately on stop, and handle shared-NAT IPsec endpoint reuse on newer Linux kernels. Fix EDNS fallback and serialize configuration writes. Upgrade during a maintenance window because mappings restart.",
		"zh-TW": "1.0.5：修復 PPP 准入及防火牆規則偏移，停止時立即撤銷存取權限，並修正較新 Linux 核心上的共享 NAT IPsec 端點回收。修復 EDNS 備援和並行設定寫入。升級會重啟映射，請安排維護時段。",
		"zh-CN": "1.0.5：修复 PPP 准入和防火墙规则漂移，停止时立即撤销访问权限，并修复新版 Linux 上的共享 NAT IPsec 端点回收。修复 EDNS 备用切换和并发配置写入。升级会重启映射，请安排维护窗口。",
	}
	if regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

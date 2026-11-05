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
		"en":    "Reissued 1.0.2: CLI upgrades run independently of SSH, select the stable release and pin its commit. Package compilation is serialized with progress output and a saved result. CLI menu 14 switches English, Traditional Chinese or Simplified Chinese through the same setting as Web, without restarting services. Keep L2TP with optional L2TP/IPsec and explicit SOCKS5 or local WAN egress; no automatic SOCKS5-to-WAN fallback. Remove IKEv2 site-to-site configuration before upgrading. Application upgrades still restart mappings and require clients to reconnect.",
		"zh-TW": "重新發佈 1.0.2：命令列升級獨立於 SSH 執行，選擇穩定版並固定提交；編譯按套件依序執行，顯示進度並保存結果。選單 14 可切換英語、繁體中文、簡體中文，與 Web 共用設定，不重啟服務。保留 L2TP、可選的 L2TP/IPsec，以及明確選擇的 SOCKS5 或本機 WAN 出口；SOCKS5 不會自動回落到 WAN。升級前須移除 IKEv2 站點間設定。程式升級仍會重建映射，客戶端須重新連線。",
		"zh-CN": "重新发布 1.0.2：命令行升级独立于 SSH 运行，选择稳定版并固定提交；编译按包串行执行，显示进度并保存结果。菜单 14 可切换英语、繁体中文、简体中文，与 Web 共用设置，不重启服务。保留 L2TP、可选的 L2TP/IPsec，以及明确选择的 SOCKS5 或本机 WAN 出口；SOCKS5 不会自动回落到 WAN。升级前须移除 IKEv2 站点间配置。程序升级仍会重建映射，客户端须重新连接。",
	}
	if regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

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
		"en":    "Keep L2TP with optional L2TP/IPsec; remove IKEv2 site-to-site configuration. Add explicit local WAN egress for L2TP mappings, with application traffic, DNS and probes bound to the selected interface; SOCKS5 failures never fall back to WAN. Reject WAN/TUN name conflicts, isolate slow PPP return-route synchronization, and use TCP DNS for WAN hostname bootstrap. Upgrades restart mappings and require clients to reconnect. Existing IKEv2 site configurations must be removed before upgrading.",
		"zh-TW": "保留 L2TP 與可選的 L2TP/IPsec，移除 IKEv2 站點間設定。L2TP 映射可明確選用本機 WAN，應用流量、DNS 與探測綁定指定網卡；SOCKS5 故障不會回落到 WAN。拒絕 WAN/TUN 名稱衝突，限制慢速 PPP 回程路由同步的影響，WAN 網域引導解析改用 TCP DNS。升級會重建映射，客戶端須重新連線；已有 IKEv2 站點設定須先移除。",
		"zh-CN": "保留 L2TP 与可选的 L2TP/IPsec，移除 IKEv2 站点间配置。L2TP 映射可明确选用本机 WAN，应用流量、DNS 与探测绑定指定网卡；SOCKS5 故障不会回落到 WAN。拒绝 WAN/TUN 名称冲突，限制慢速 PPP 回程路由同步的影响，WAN 域名引导解析改用 TCP DNS。升级会重建映射，客户端须重新连接；已有 IKEv2 站点配置须先移除。",
	}
	if regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

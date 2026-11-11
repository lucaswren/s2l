package buildinfo

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// Populated by the managed installation and upgrade scripts.
var Version = "development"
var Revision = ""

var stableVersion = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)$`)

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
	switch strings.TrimPrefix(result.Version, "v") {
	case "1.0.7":
		result.ReleaseNotes = map[string]string{
			"en":    "1.0.7: Restore TCP DNS preference after two successful live TCP answers when DoH fails or stalls. Keep local pool waits and caller deadlines out of upstream failure diagnostics. Upgrade during a maintenance window because mappings restart.",
			"zh-TW": "1.0.7：DoH 失敗或遲遲未回覆時，連續兩次實際 TCP DNS 查詢成功後恢復優先使用 TCP。本機連線池等候逾時與呼叫端逾時不再計入上游故障。升級會重啟映射，請安排維護時段。",
			"zh-CN": "1.0.7：DoH 失败或迟迟未返回时，连续两次实际 TCP DNS 查询成功后恢复优先使用 TCP。本地连接池排队超时与调用方超时不再计入上游故障。升级会重启映射，请安排维护窗口。",
		}
	case "1.0.6":
		result.ReleaseNotes = map[string]string{
			"en":    "1.0.6: Prefer TCP DNS at 8.8.8.8 with automatic DoH fallback through the same SOCKS5 exit. Block stray packets sourced from the TUN address, reduce repeated firewall reads and improve DNS and UDP diagnostics. Upgrade during a maintenance window because mappings restart.",
			"zh-TW": "1.0.6：優先使用 8.8.8.8 的 TCP DNS，自動經同一 SOCKS5 出口切換至 DoH。阻擋以 TUN 位址為來源的異常封包外送，減少重複讀取防火牆規則，改善 DNS 與 UDP 診斷。升級會重啟映射，請安排維護時段。",
			"zh-CN": "1.0.6：优先使用 8.8.8.8 的 TCP DNS，自动经同一 SOCKS5 出口切换至 DoH。阻止以 TUN 地址为来源的异常数据包直出，减少重复读取防火墙规则，改进 DNS 与 UDP 诊断。升级会重启映射，请安排维护窗口。",
		}
	case "1.0.5":
		result.ReleaseNotes = map[string]string{
			"en":    "1.0.5: Repair PPP admission and firewall drift, revoke access immediately on stop, and handle shared-NAT IPsec endpoint reuse on newer Linux kernels. Fix EDNS fallback and serialize configuration writes. Upgrade during a maintenance window because mappings restart.",
			"zh-TW": "1.0.5：修復 PPP 准入及防火牆規則偏移，停止時立即撤銷存取權限，並修正較新 Linux 核心上的共享 NAT IPsec 端點回收。修復 EDNS 備援和並行設定寫入。升級會重啟映射，請安排維護時段。",
			"zh-CN": "1.0.5：修复 PPP 准入和防火墙规则漂移，停止时立即撤销访问权限，并修复新版 Linux 上的共享 NAT IPsec 端点回收。修复 EDNS 备用切换和并发配置写入。升级会重启映射，请安排维护窗口。",
		}
	}
	if stableVersion.MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

// IsOlderStableRelease only orders stable tags. Source and development builds
// may still be replaced with a reviewed stable release.
func IsOlderStableRelease(candidate, current string) bool {
	a, b := stableVersion.FindStringSubmatch(candidate), stableVersion.FindStringSubmatch(current)
	if a == nil || b == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		left, right := strings.TrimLeft(a[i], "0"), strings.TrimLeft(b[i], "0")
		if len(left) != len(right) {
			return len(left) < len(right)
		}
		if left != right {
			return left < right
		}
	}
	return false
}

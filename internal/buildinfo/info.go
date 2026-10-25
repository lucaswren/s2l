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
		"zh-CN": "补齐 PPP DNS 下发、自定义 DoH/TCP DNS 和客户端接入指引；支持同一 NAT 下多个 L2TP/IPsec 客户端使用源端口 1701，内部端点经隔离与完整空闲核验后自动回收。固定 SOCKS5 出口、UDP 策略及上游/TUN 中断时的防泄漏保护保持有效；阻断 PPP 断开后迟到的 TUN 回包经默认出口泄漏。升级在停止服务前自动补装缺少的 conntrack，失败时保留现有服务；保留 Ubuntu AppArmor 约束并允许读取受管 IPsec 密钥，修复 CI 启动超时与 UDP 拒绝验收时序。Linux 实测覆盖并发、重拨、DNS 和 UDP，Windows/macOS 原生拨号尚未验证。",
		"zh-TW": "補齊 PPP DNS 下發、自訂 DoH/TCP DNS 與用戶端連線指引；支援同一 NAT 下多個 L2TP/IPsec 用戶端使用來源埠 1701，內部端點經隔離及完整閒置核驗後自動回收。固定 SOCKS5 出口、UDP 策略與上游/TUN 中斷時的防洩漏保護維持有效；阻斷 PPP 中斷後遲到的 TUN 回包經預設出口洩漏。升級在停止服務前自動補裝缺少的 conntrack，失敗時保留既有服務；保留 Ubuntu AppArmor 約束並允許讀取受管 IPsec 密鑰，修復 CI 啟動逾時與 UDP 拒絕驗收時序。Linux 實測涵蓋並行、重撥、DNS 與 UDP，Windows/macOS 原生撥號尚未驗證。",
		"en":    "Add PPP DNS advertisement, custom DoH/TCP resolvers and client setup guidance. Isolate L2TP/IPsec clients sharing one NAT and source port 1701; automatically reclaim internal endpoints after quarantine and complete idle checks. Preserve fixed SOCKS5 exits, UDP policies and leak protection during upstream/TUN failures; block delayed TUN replies from escaping through the default uplink after PPP disconnects. Install missing conntrack before stopping services; dependency failures leave existing services running. Allow managed IPsec secret reads while retaining Ubuntu AppArmor enforcement; fix CI boot retries and UDP rejection probe timing. Linux live tests cover concurrency, redial, DNS and UDP; native Windows/macOS dialing remains unverified.",
	}
	if regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

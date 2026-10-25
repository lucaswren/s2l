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
		"zh-CN": "修复首次安装 HTTPS 的就绪竞态：等待后端与实际 TLS 接口可用，检查证书和私钥匹配，随机管理端口避开系统临时端口范围。新增映射先准备 TUN、路由、NAT 和防火墙，再发布 L2TP 账号；修复 rp_filter 漂移与 xl2tpd 重启后遗留 PPP 进程。修复回滚后立即重装时 TCP TIME_WAIT 被误判为端口占用；抑制安装阶段 needrestart 自动重启服务，改善 Go 下载停滞处理。统一安装入口，清理冗余代码及浏览器测试临时数据。升级需重拨；Windows/macOS 原生拨号尚未验证。",
		"zh-TW": "修復首次安裝 HTTPS 的就緒競態：等待後端與實際 TLS 介面可用，檢查憑證與私鑰相符，隨機管理埠避開系統暫時埠範圍。新增映射先準備 TUN、路由、NAT 與防火牆，再發佈 L2TP 帳號；修復 rp_filter 漂移與 xl2tpd 重啟後遺留 PPP 程序。修復回復後立即重新安裝時 TCP TIME_WAIT 被誤判為埠占用；抑制安裝階段 needrestart 自動重啟服務，改善 Go 下載停滯處理。統一安裝入口，清理冗餘程式碼及瀏覽器測試暫存資料。升級後須重新撥號；Windows/macOS 原生撥號尚未驗證。",
		"en":    "Fix first-install HTTPS readiness races by waiting for the backend and pinned TLS endpoint, checking certificate/key consistency and excluding ephemeral ports from random management-port selection. Prepare TUN, routing, NAT and firewall rules before publishing L2TP credentials; repair rp_filter drift and prevent orphan PPP processes after xl2tpd restarts. Allow immediate reinstall after rollback despite TCP TIME_WAIT; suppress needrestart-driven service restarts during installation and recover sooner from stalled Go downloads. Consolidate the installer and remove unused code and disposable browser-test data. Clients must redial after upgrading; native Windows/macOS dialing remains unverified.",
	}
	if regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

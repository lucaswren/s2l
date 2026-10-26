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
		"en":    "Unify installation language selection with --lang en|zh-Hant|zh-Hans. Avoid redundant bootstrap package downloads and show build/HTTPS timings. Reject unsafe source checkouts and recover owned return guards after failed installation. Preserve distinct SOCKS5 credentials at shared addresses, reject ambiguous imports and retain text-import metadata. Show linked mappings before credential edits. Repair PPP firewall priority without deleting foreign rules and keep IPsec admission behind PPP management/isolation. Remove unnecessary proxy ARP. Expand multilingual operations and router guidance. Verified with Ubuntu 24.04 and Debian 12 full VM installation, import, real PPP, reboot and failure recovery; ARM64 is build-only. Application upgrades restart services; schedule a maintenance window.",
		"zh-TW": "統一以 --lang en|zh-Hant|zh-Hans 選擇安裝語言，減少重複套件下載並顯示編譯／HTTPS 耗時。拒絕不安全的原始碼工作副本，修復安裝失敗後的返回防護清理。共用 SOCKS5 位址的不同憑證保留為獨立節點，拒絕有歧義的匯入並保留文字匯入中繼資料；編輯憑證先顯示關聯映射。修復 PPP 規則優先順序而不刪除外部規則，IPsec 入口保留在 PPP 管理／隔離之後。移除不必要的 proxy ARP，補充多語操作與路由器指南。通過 Ubuntu 24.04、Debian 12 完整虛擬機安裝、匯入、真實 PPP、重啟與故障復原驗證；ARM64 僅驗證建置。應用程式升級會重啟服務，請安排維護時段。",
		"zh-CN": "统一以 --lang en|zh-Hant|zh-Hans 选择安装语言，减少重复软件下载并显示编译／HTTPS 耗时。拒绝不安全的源码工作副本，修复安装失败后的返回防护清理。同一 SOCKS5 地址的不同凭据保留为独立节点，拒绝有歧义的导入并保留文本导入元数据；编辑凭据先显示关联映射。修复 PPP 规则优先顺序而不删除外部规则，IPsec 入口保留在 PPP 管理／隔离之后。移除不必要的 proxy ARP，补充多语言操作与路由器指南。通过 Ubuntu 24.04、Debian 12 完整虚拟机安装、导入、真实 PPP、重启与故障恢复验证；ARM64 仅验证构建。应用升级会重启服务，请安排维护时段。",
	}
	if regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

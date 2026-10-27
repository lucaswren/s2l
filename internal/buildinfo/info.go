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
		"en":    "Add s2l uninstall (menu 13) and s2l uninstall --dry-run. Show scope and require UNINSTALL confirmation, create a private backup, then stop VPN services and remove only managed application, PPP, HTTPS and network resources. Refuse SSH paths through the VPN, custom layouts, conflicting routes and unfinished operations. Retain SSH settings, other firewall rules and Nginx sites, dependencies, kernels and source checkouts. Uninstall disconnects VPN clients; failed cleanup retains the backup and may leave services stopped. Prompts follow the saved panel language.",
		"zh-TW": "新增 s2l uninstall（選單 13）及 s2l uninstall --dry-run 只讀預覽。顯示範圍並要求輸入 UNINSTALL，建立私有備份後停止 VPN 服務，僅清理受管應用程式、PPP、HTTPS 與網路資源。拒絕經 VPN 的 SSH 路徑、自訂部署、衝突路由及未完成操作。保留 SSH 設定、其他防火牆規則與 Nginx 站台、相依套件、核心及原始碼。解除安裝會中斷 VPN；清理失敗保留備份，服務可能維持停止。提示沿用面板語言。",
		"zh-CN": "新增 s2l uninstall（菜单 13）及 s2l uninstall --dry-run 只读预览。显示范围并要求输入 UNINSTALL，建立私有备份后停止 VPN 服务，仅清理受管应用、PPP、HTTPS 与网络资源。拒绝经 VPN 的 SSH 路径、自定义部署、冲突路由及未完成操作。保留 SSH 设置、其他防火墙规则与 Nginx 站点、依赖、内核及源码。卸载会中断 VPN；清理失败保留备份，服务可能保持停止。提示沿用面板语言。",
	}
	if regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

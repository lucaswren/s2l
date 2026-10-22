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
		"zh-CN": "降低后台巡检与 DNS 连接开销，按需启用连接观测，原位修复缺失规则；增加网段和系统资源冲突检查、固定地址账号单会话保护、安装预检、依赖校验与失败恢复。两个独立账号共用节点的并发拨号、故障隔离和整机重启测试通过。固定出口、DoH/TCP、UDP 主动拒绝及防泄漏保护保持不变。",
		"zh-TW": "降低背景巡檢與 DNS 連線負擔，按需啟用連線觀測，原位修復遺失規則；增加網段與系統資源衝突檢查、固定地址帳號單會話保護、安裝預檢、相依性驗證與失敗恢復。兩個獨立帳號共用節點的並行撥號、故障隔離與整機重啟測試通過。固定出口、DoH/TCP、UDP 主動拒絕與防洩漏保護保持不變。",
		"en":    "Reduce background checks and reuse DNS connections; make connection observation optional and repair missing rules in place. Add subnet and host-resource validation, single-session protection for fixed peer addresses, installation preflight, dependency checks and failure recovery. Independent accounts sharing one node pass concurrent PPP, fault-isolation and full-reboot tests. Fixed exits, DoH/TCP, active UDP rejection and leak protection remain enabled.",
	}
	if regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

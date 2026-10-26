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
		"en":    "Add s2l kernel-upgrade (menu 12) to prepare Ubuntu 24.04 HWE kernels with host, package, boot-file and module checks, private GRUB backups and operation locks. Preserve old kernels and require a separate manual reboot. Fresh installation detects known Ubuntu 6.8.0-146/147 L2TP regressions, prepares a replacement and exits 20 until reboot; repeated attempts reuse complete kernel files. Order languages as English, Traditional Chinese, Simplified Chinese. Application upgrades still restart services; kernel preparation does not reboot the server or prove compatibility on every host.",
		"zh-TW": "新增 s2l kernel-upgrade（選單 12），檢查系統、套件、啟動檔案與模組，私有備份 GRUB 設定並鎖定並行操作，準備 Ubuntu 24.04 HWE 核心。保留舊核心，須另外人工重啟。首次安裝偵測 Ubuntu 6.8.0-146/147 已知 L2TP 回歸，準備替代核心後以退出碼 20 等待重啟；重跑會重用完整核心檔案。語言依英文、繁體中文、簡體中文排序。S2L 應用程式升級仍會重啟服務；核心準備不會重啟伺服器，也不代表所有主機均已驗證相容。",
		"zh-CN": "新增 s2l kernel-upgrade（菜单 12），检查系统、软件包、启动文件与模块，私有备份 GRUB 设置并锁定并发操作，准备 Ubuntu 24.04 HWE 内核。保留旧内核，需要另行人工重启。首次安装检测 Ubuntu 6.8.0-146/147 已知 L2TP 回归，准备替代内核后以退出码 20 等待重启；重跑会复用完整内核文件。语言按英语、繁体中文、简体中文排序。S2L 应用升级仍会重启服务；内核准备不会重启服务器，也不代表所有主机均已验证兼容。",
	}
	if regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(result.Version) {
		result.ReleaseURL = "https://github.com/lucaswren/s2l/releases/tag/" + result.Version
	}
	return result
}

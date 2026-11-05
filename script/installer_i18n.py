"""Installer and management CLI messages. Installation uses install.sh --lang.

The private environment value carries that choice into child processes. Helpers
called without an explicit language keep their source messages for API consumers.
"""
import os

# Source message -> English, Traditional Chinese, Simplified Chinese.
MESSAGES = {
    '{protocol} port {port} is unavailable: {detail}': ('{protocol} port {port} is unavailable: {detail}', '{protocol} 連接埠 {port} 無法使用：{detail}', '{protocol} 端口 {port} 无法使用：{detail}'),
    '端口需为 1-65535 的整数': ('Port must be an integer from 1 to 65535', '連接埠須為 1–65535 的整數', '端口需为 1-65535 的整数'),
    '临时端口范围覆盖全部随机管理端口，请显式指定端口': ('Ephemeral port range covers all random management ports; specify a port explicitly', '暫用連接埠範圍涵蓋所有隨機管理埠，請明確指定連接埠', '临时端口范围覆盖全部随机管理端口，请显式指定端口'),
    '无法找到空闲端口': ('Cannot find an available port', '找不到可用連接埠', '无法找到空闲端口'),
    '修改失败：{error}': ('Configuration failed: {error}', '修改失敗：{error}', '修改失败：{error}'),
    'PUBLIC_IP 必须为公网 IPv4 地址': ('PUBLIC_IP must be a public IPv4 address', 'PUBLIC_IP 必須為公網 IPv4 位址', 'PUBLIC_IP 必须为公网 IPv4 地址'),
    '无法识别公网 IPv4；请设置 PUBLIC_IP=服务器公网IPv4 后重新安装': ('Cannot detect a public IPv4 address; set PUBLIC_IP and retry', '無法偵測公網 IPv4；請設定 PUBLIC_IP 後重試', '无法识别公网 IPv4；请设置 PUBLIC_IP 后重试'),
    'Go 需要 1.22 或更新版本；请安装兼容工具链后重试': ('Go 1.22 or later is required; install a compatible toolchain and retry', '需要 Go 1.22 或更新版本；請安裝相容工具鏈後重試', 'Go 需要 1.22 或更新版本；请安装兼容工具链后重试'),
    '仅支持已验证的 sing-box 1.11.15；已有程序不会被自动覆盖': ('Only verified sing-box 1.11.15 is supported; existing binaries will not be overwritten', '僅支援已驗證的 sing-box 1.11.15；不會自動覆寫現有程式', '仅支持已验证的 sing-box 1.11.15；已有程序不会被自动覆盖'),
    '当前 Ubuntu 内核存在已知 L2TP 崩溃问题（2169477）；请切换已修复内核或 HWE 内核并重启后重试': ('This Ubuntu kernel has a known L2TP crash (2169477); boot a fixed or HWE kernel and retry', '目前 Ubuntu 核心有已知 L2TP 當機問題（2169477）；請切換已修復或 HWE 核心並重新啟動後重試', '当前 Ubuntu 内核存在已知 L2TP 崩溃问题（2169477）；请切换已修复内核或 HWE 内核并重启后重试'),
    'requires Linux root': ('Requires Linux root', '需要 Linux root 權限', '需要 Linux root 权限'),
    'requires root': ('Requires root', '需要 root 權限', '需要 root 权限'),
    'installer supports Ubuntu and Debian only': ('Installer supports Ubuntu and Debian only', '安裝器僅支援 Ubuntu 與 Debian', '安装器仅支持 Ubuntu 和 Debian'),
    'systemd must be running as the system service manager': ('systemd must be the running system service manager', '必須使用 systemd 管理系統服務', '必须使用 systemd 管理系统服务'),
    'installation target must not be a symlink: ': ('Installation target must not be a symlink: ', '安裝目標不能是符號連結：', '安装目标不能是符号链接：'),
    'S2L is already installed; use the upgrade command': ('S2L is already installed; use the upgrade command', 'S2L 已安裝；請使用升級指令', 'S2L 已安装；请使用升级命令'),
    '/opt/s2l contains existing files; refusing to overwrite': ('/opt/s2l contains existing files; refusing to overwrite', '/opt/s2l 已有檔案；拒絕覆寫', '/opt/s2l 已有文件；拒绝覆盖'),
    '/usr/local/bin/s2l is already in use': ('/usr/local/bin/s2l is already in use', '/usr/local/bin/s2l 已被使用', '/usr/local/bin/s2l 已被使用'),
    'existing PPP accounts detected; use a dedicated host': ('Existing PPP accounts detected; use a dedicated host', '偵測到現有 PPP 帳號；請使用專用主機', '检测到现有 PPP 账号；请使用专用主机'),
    'existing PPP sessions detected; use a dedicated host': ('Existing PPP sessions detected; use a dedicated host', '偵測到現有 PPP 連線；請使用專用主機', '检测到现有 PPP 会话；请使用专用主机'),
    'existing tunnel service is active: ': ('Existing tunnel service is active: ', '現有隧道服務正在執行：', '现有隧道服务正在运行：'),
    'existing PPP interfaces detected; use a dedicated host': ('Existing PPP interfaces detected; use a dedicated host', '偵測到現有 PPP 介面；請使用專用主機', '检测到现有 PPP 接口；请使用专用主机'),
    'existing S2L firewall state detected; recover previous installation first': ('Existing S2L firewall state detected; recover the previous installation first', '偵測到現有 S2L 防火牆狀態；請先復原上次安裝', '检测到现有 S2L 防火墙状态；请先恢复上次安装'),
    'unknown preflight check': ('Unknown preflight check', '未知的安裝前檢查', '未知的安装前检查'),
    'unexpected Go archive entry': ('Unexpected Go archive entry', 'Go 壓縮檔含非預期項目', 'Go 压缩包含非预期条目'),
    'Go archive root must be a directory': ('Go archive root must be a directory', 'Go 壓縮檔根路徑必須為目錄', 'Go 压缩包根路径必须为目录'),
    '/usr/local/go already exists; refusing to overwrite': ('/usr/local/go already exists; refusing to overwrite', '/usr/local/go 已存在；拒絕覆寫', '/usr/local/go 已存在；拒绝覆盖'),
    'no supported stable Go archive available': ('No supported stable Go archive available', '找不到支援的 Go 穩定版壓縮檔', '找不到支持的 Go 稳定版压缩包'),
    'invalid Go release metadata': ('Invalid Go release metadata', 'Go 發行版本資料無效', 'Go 发布版本信息无效'),
    'Go archive checksum mismatch': ('Go archive checksum mismatch', 'Go 壓縮檔雜湊值不符', 'Go 压缩包校验值不匹配'),
    'installation journal must not be a symlink': ('Installation journal must not be a symlink', '安裝日誌不能為符號連結', '安装日志不能为符号链接'),
    'installation journal must be root-owned with mode 0700': ('Installation journal must be root-owned with mode 0700', '安裝日誌必須由 root 擁有且權限為 0700', '安装日志必须由 root 所有且权限为 0700'),
    'recover the previous installation first': ('Recover the previous installation first', '請先復原上次安裝', '请先恢复上次安装'),
    '/opt/s2l is not an empty installation directory': ('/opt/s2l is not an empty installation directory', '/opt/s2l 不是空的安裝目錄', '/opt/s2l 不是空的安装目录'),
    'unexpected installation target: ': ('Unexpected installation target: ', '非預期的安裝目標：', '非预期的安装目标：'),
    'cannot snapshot network sysctl settings': ('Cannot snapshot network sysctl settings', '無法備份網路 sysctl 設定', '无法备份网络 sysctl 设置'),
    'invalid installation journal; refusing recovery': ('Invalid installation journal; refusing recovery', '安裝日誌無效；拒絕復原', '安装日志无效；拒绝恢复'),
    'installation directory was replaced; refusing recovery': ('Installation directory was replaced; refusing recovery', '安裝目錄已被替換；拒絕復原', '安装目录已被替换；拒绝恢复'),
    'installation ownership changed; refusing recovery': ('Installation ownership changed; refusing recovery', '安裝所有權已變更；拒絕復原', '安装所有权已变更；拒绝恢复'),
    'PPP sessions still exist; leak guards retained for manual recovery': ('PPP sessions still exist; leak guards retained for manual recovery', 'PPP 連線仍存在；保留防洩漏規則，請手動復原', 'PPP 会话仍存在；保留防泄漏规则，请手动恢复'),
    'invalid snapshot kind': ('Invalid snapshot kind', '無效的備份類型', '无效的备份类型'),
    'invalid sysctl snapshot': ('Invalid sysctl snapshot', '無效的 sysctl 備份', '无效的 sysctl 备份'),
    '安装未完成，已恢复安装前配置；依赖软件包保留，可重新执行安装。': ('Installation incomplete; original configuration restored. Dependencies retained; you can retry installation.', '安裝未完成，已復原安裝前設定；相依套件保留，可重新安裝。', '安装未完成，已恢复安装前配置；依赖软件包保留，可重新执行安装。'),
    '首次安装请使用完整源码中的 install.sh；已有主机使用 s2l kernel-upgrade': ('For fresh installation use install.sh from the full source; on installed hosts use s2l kernel-upgrade', '首次安裝請使用完整原始碼的 install.sh；現有主機使用 s2l kernel-upgrade', '首次安装请使用完整源码中的 install.sh；已有主机使用 s2l kernel-upgrade'),
    '需要 Linux root；请使用 sudo s2l kernel-upgrade': ('Linux root is required; use sudo s2l kernel-upgrade', '需要 Linux root；請使用 sudo s2l kernel-upgrade', '需要 Linux root；请使用 sudo s2l kernel-upgrade'),
    '需要以 systemd 运行的 Ubuntu 24.04 主机': ('Requires an Ubuntu 24.04 host running systemd', '需要以 systemd 執行的 Ubuntu 24.04 主機', '需要以 systemd 运行的 Ubuntu 24.04 主机'),
    '目标内核无效或仍有已知 L2TP 问题: ': ('Target kernel is invalid or has a known L2TP issue: ', '目標核心無效或仍有已知 L2TP 問題：', '目标内核无效或仍有已知 L2TP 问题：'),
    '目标内核软件包未配置完成: ': ('Target kernel package configuration is incomplete: ', '目標核心套件尚未設定完成：', '目标内核软件包未配置完成：'),
    '缺少目标内核启动文件: ': ('Missing target kernel boot file: ', '缺少目標核心啟動檔案：', '缺少目标内核启动文件：'),
    '目标内核缺少模块: ': ('Target kernel lacks module: ', '目標核心缺少模組：', '目标内核缺少模块：'),
    'GRUB 中未找到目标内核启动项: ': ('Target kernel boot entry not found in GRUB: ', 'GRUB 找不到目標核心啟動項目：', 'GRUB 中未找到目标内核启动项：'),
    '自动准备内核仅支持标准 GRUB 主机；请按手动内核升级文档操作': ('Automatic kernel preparation requires standard GRUB; follow the manual kernel upgrade guide', '自動準備核心僅支援標準 GRUB；請依手動核心升級文件操作', '自动准备内核仅支持标准 GRUB 主机；请按手动内核升级文档操作'),
    'GRUB 使用自定义默认启动项；请手动选择内核，安装器不会覆盖启动选择': ('GRUB has a custom default entry; select the kernel manually. The installer will not overwrite it.', 'GRUB 使用自訂預設啟動項目；請手動選擇核心，安裝器不會覆寫', 'GRUB 使用自定义默认启动项；请手动选择内核，安装器不会覆盖启动选择'),
    '[WARN] GRUB 菜单可能隐藏；重启前确认主机商控制台和旧内核回退入口可用': ('[WARN] GRUB may be hidden; ensure console access and old-kernel rollback before reboot', '[WARN] GRUB 選單可能隱藏；重啟前請確認控制台及舊核心回復入口可用', '[WARN] GRUB 菜单可能隐藏；重启前确认主机商控制台和旧内核回退入口可用'),
    '{path} 可用空间不足，至少需要 {minimum} MiB；请手动处理，安装器不会删除旧内核': ('{path} needs at least {minimum} MiB free; resolve manually. Old kernels will not be deleted.', '{path} 至少需要 {minimum} MiB 可用空間；請手動處理，不會刪除舊核心', '{path} 可用空间不足，至少需要 {minimum} MiB；请手动处理，安装器不会删除旧内核'),
    '存在未完成的软件包配置；请先检查 dpkg --audit，再重试内核升级': ('Package configuration is incomplete; check dpkg --audit before retrying kernel upgrade', '套件設定尚未完成；請先檢查 dpkg --audit 再重試核心升級', '存在未完成的软件包配置；请先检查 dpkg --audit，再重试内核升级'),
    '[INFO] 启动配置备份: ': ('[INFO] Boot configuration backup: ', '[INFO] 啟動設定備份：', '[INFO] 启动配置备份：'),
    '[INFO] 内核启动文件与 PPP/L2TP/TUN 模块已检查: ': ('[INFO] Kernel boot files and PPP/L2TP/TUN modules verified: ', '[INFO] 已檢查核心啟動檔案及 PPP/L2TP/TUN 模組：', '[INFO] 内核启动文件与 PPP/L2TP/TUN 模块已检查：'),
    '[OK] 新内核已准备，等待人工重启生效；现有 S2L 配置无需重装。': ('[OK] Kernel prepared; reboot manually to activate. S2L does not need reinstalling.', '[OK] 新核心已準備，等待手動重啟生效；S2L 無需重新安裝。', '[OK] 新内核已准备，等待人工重启生效；现有 S2L 配置无需重装。'),
    '[WARN] 尚未切换正在运行的内核；S2L 安装未完成，退出码 20（等待人工重启）。': ('[WARN] Running kernel unchanged; S2L installation incomplete. Exit 20: manual reboot required.', '[WARN] 尚未切換執行中的核心；S2L 安裝未完成，退出碼 20（等待手動重啟）。', '[WARN] 尚未切换正在运行的内核；S2L 安装未完成，退出码 20（等待人工重启）。'),
    '[WARN] 重启会断开 SSH/VPN。确认维护窗口与控制台回退入口后，手动执行 sudo reboot。': ('[WARN] Reboot disconnects SSH/VPN. Confirm a maintenance window and console rollback, then run sudo reboot manually.', '[WARN] 重啟會中斷 SSH/VPN。確認維護時段及控制台回復入口後，手動執行 sudo reboot。', '[WARN] 重启会断开 SSH/VPN。确认维护窗口与控制台回退入口后，手动执行 sudo reboot。'),
    '[INFO] 重连后检查 uname -r，并确认 L2TP、DNS 和出口恢复。': ('[INFO] After reconnecting check uname -r, L2TP, DNS and egress.', '[INFO] 重新連線後檢查 uname -r，並確認 L2TP、DNS 和出口恢復。', '[INFO] 重连后检查 uname -r，并确认 L2TP、DNS 和出口恢复。'),
    '[INFO] 重启后检查 uname -r，然后使用原参数重新运行安装命令；安装器不会自动续装或保存密码。': ('[INFO] After reboot check uname -r and rerun the original installation command with the same options; no automatic resume or password saving.', '[INFO] 重啟後檢查 uname -r，再以原參數重新執行安裝；不會自動續裝或儲存密碼。', '[INFO] 重启后检查 uname -r，然后使用原参数重新运行安装命令；安装器不会自动续装或保存密码。'),
    '[INFO] 安装的内核软件包会保留；未进行真实拨号验证，不代表所有新内核均兼容。': ('[INFO] Installed kernel packages are retained; no real dial test was run, so compatibility is not guaranteed.', '[INFO] 已安裝的核心套件會保留；尚未執行真實撥號驗證，不代表所有新核心皆相容。', '[INFO] 安装的内核软件包会保留；未进行真实拨号验证，不代表所有新内核均兼容。'),
    'S2L_KERNEL_UPGRADE 只能为 auto 或 off': ('S2L_KERNEL_UPGRADE must be auto or off', 'S2L_KERNEL_UPGRADE 只能為 auto 或 off', 'S2L_KERNEL_UPGRADE 只能为 auto 或 off'),
    '[INFO] 当前内核: ': ('[INFO] Current kernel: ', '[INFO] 目前核心：', '[INFO] 当前内核：'),
    '[INFO] 未命中已知问题内核；继续安装（不代表所有内核均已验证）。': ('[INFO] No known kernel regression matched; continuing installation (not all kernels are verified).', '[INFO] 未命中已知問題核心；繼續安裝（不代表所有核心皆已驗證）。', '[INFO] 未命中已知问题内核；继续安装（不代表所有内核均已验证）。'),
    '[WARN] 已知 L2TP 内核问题 2169477；当前: ': ('[WARN] Known L2TP kernel issue 2169477; running: ', '[WARN] 已知 L2TP 核心問題 2169477；目前：', '[WARN] 已知 L2TP 内核问题 2169477；当前：'),
    '，已安装的最高 generic 内核: ': ('; highest installed generic kernel: ', '，已安裝的最高 generic 核心：', '，已安装的最高 generic 内核：'),
    '已关闭自动准备内核；请手动切换兼容内核后重新运行安装。S2L 尚未部署。': ('Automatic kernel preparation is off; boot a compatible kernel and rerun installation. S2L is not deployed.', '已關閉自動準備核心；請手動切換相容核心後重新安裝。S2L 尚未部署。', '已关闭自动准备内核；请手动切换兼容内核后重新运行安装。S2L 尚未部署。'),
    '该系统没有受支持的自动内核升级路径；请手动处理后重试': ('No supported automatic kernel upgrade path for this system; resolve manually and retry', '此系統沒有支援的自動核心升級路徑；請手動處理後重試', '该系统没有受支持的自动内核升级路径；请手动处理后重试'),
    '容器不能更换宿主机内核；请在宿主机处理': ('Containers cannot replace the host kernel; update it on the host', '容器不能更換宿主機核心；請在宿主機處理', '容器不能更换宿主机内核；请在宿主机处理'),
    '无法确认主机是否为容器；请手动检查后重试': ('Cannot determine whether this is a container; check manually and retry', '無法確認主機是否為容器；請手動檢查後重試', '无法确认主机是否为容器；请手动检查后重试'),
    '[WARN] 已安装的新内核尚未通过完整性检查: ': ('[WARN] Installed new kernel failed integrity checks: ', '[WARN] 已安裝的新核心尚未通過完整性檢查：', '[WARN] 已安装的新内核尚未通过完整性检查：'),
    '[INFO] 从系统 APT 源准备 Ubuntu 24.04 HWE 内核；保留旧内核，不自动重启。': ('[INFO] Preparing Ubuntu 24.04 HWE from system APT sources; retaining old kernels, no automatic reboot.', '[INFO] 從系統 APT 來源準備 Ubuntu 24.04 HWE 核心；保留舊核心，不自動重啟。', '[INFO] 从系统 APT 源准备 Ubuntu 24.04 HWE 内核；保留旧内核，不自动重启。'),
    'APT 未提供更新的 generic 内核；请检查软件源，勿继续部署 S2L': ('APT offered no newer generic kernel; check sources before deploying S2L', 'APT 未提供更新的 generic 核心；請檢查套件來源，勿繼續部署 S2L', 'APT 未提供更新的 generic 内核；请检查软件源，勿继续部署 S2L'),
    '[OK] 当前已运行 APT 提供的目标内核，无需为此次操作重启。': ('[OK] Already running the target kernel from APT; no reboot required for this operation.', '[OK] 已執行 APT 提供的目標核心，此次操作無需重啟。', '[OK] 当前已运行 APT 提供的目标内核，无需为此次操作重启。'),
    '准备 Ubuntu 24.04 HWE 内核；保留旧内核，不自动重启。': ('Prepare Ubuntu 24.04 HWE; retain old kernels, no automatic reboot.', '準備 Ubuntu 24.04 HWE 核心；保留舊核心，不自動重啟。', '准备 Ubuntu 24.04 HWE 内核；保留旧内核，不自动重启。'),
    '维护已有服务器；省略时执行首次安装检查': ('Maintain an installed server; omit for fresh-install checks', '維護現有伺服器；省略時執行首次安裝檢查', '维护已有服务器；省略时执行首次安装检查'),
    '另一个安装或升级正在运行，请完成后重试': ('Another installation or upgrade is running; wait and retry', '另一個安裝或升級正在執行，請完成後重試', '另一个安装或升级正在运行，请完成后重试'),
    '[ERROR] 内核准备未完成: ': ('[ERROR] Kernel preparation incomplete: ', '[ERROR] 核心準備未完成：', '[ERROR] 内核准备未完成：'),
    '[ERROR] 请勿重启；已安装的软件包保留。检查 dpkg --audit、启动文件和软件源后重试。': ('[ERROR] Do not reboot; installed packages retained. Check dpkg --audit, boot files and APT sources before retrying.', '[ERROR] 請勿重啟；已安裝套件保留。檢查 dpkg --audit、啟動檔案及套件來源後重試。', '[ERROR] 请勿重启；已安装的软件包保留。检查 dpkg --audit、启动文件和软件源后重试。'),
    '不支持符号链接安装目录或配置；请使用手动升级流程': ('Symlinked installation paths are unsupported; use manual upgrade', '不支援符號連結的安裝路徑；請手動升級', '不支持符号链接安装目录或配置；请使用手动升级流程'),
    '配置须为 JSON 对象': ('Configuration must be a JSON object', '設定須為 JSON 物件', '配置须为 JSON 对象'),
    '自定义文件路径不支持自动升级；请使用手动升级流程': ('Custom file paths require manual upgrade', '自訂檔案路徑需手動升級', '自定义文件路径不支持自动升级；请使用手动升级流程'),
    '管理端口无效': ('Invalid management port', '管理連接埠無效', '管理端口无效'),
    '非本机监听部署须使用手动升级流程': ('Non-loopback deployments require manual upgrade', '非本機監聽部署需手動升級', '非本机监听部署须使用手动升级流程'),
    'HTTPS 管理地址无效': ('Invalid HTTPS management address', 'HTTPS 管理位址無效', 'HTTPS 管理地址无效'),
    'HTTPS 监听证书尚未更新': ('HTTPS listener certificate has not been updated', 'HTTPS 監聽憑證尚未更新', 'HTTPS 监听证书尚未更新'),
    'HTTPS 后端返回 HTTP {status}': ('HTTPS backend returned HTTP {status}', 'HTTPS 後端回傳 HTTP {status}', 'HTTPS 后端返回 HTTP {status}'),
    'HTTPS 后端响应无效': ('Invalid HTTPS backend response', 'HTTPS 後端回應無效', 'HTTPS 后端响应无效'),
    '本机 HTTPS 验证失败: ': ('Local HTTPS verification failed: ', '本機 HTTPS 驗證失敗：', '本机 HTTPS 验证失败：'),
    '登录验证失败': ('Login verification failed', '登入驗證失敗', '登录验证失败'),
    '新服务的本机 API 验证失败；请检查服务日志': ('New service local API verification failed; check service logs', '新服務的本機 API 驗證失敗；請檢查服務日誌', '新服务的本机 API 验证失败；请检查服务日志'),
    '未知验证模式': ('Unknown verification mode', '未知驗證模式', '未知验证模式'),
    '[ERROR] 安装路径/配置验证或本机 API 检查失败；自定义部署请使用手动升级流程。': ('[ERROR] Installation path, configuration or local API verification failed; custom deployments require manual upgrade.', '[ERROR] 安裝路徑、設定或本機 API 驗證失敗；自訂部署請手動升級。', '[ERROR] 安装路径/配置验证或本机 API 检查失败；自定义部署请使用手动升级流程。'),
    '请输入公网 IPv4 地址': ('Enter a public IPv4 address', '請輸入公網 IPv4 位址', '请输入公网 IPv4 地址'),
    'HTTPS 端口不能与本机后端端口相同': ('HTTPS and local backend ports must differ', 'HTTPS 與本機後端連接埠不能相同', 'HTTPS 端口不能与本机后端端口相同'),
    '后端端口不能与 HTTPS 端口相同': ('Backend and HTTPS ports must differ', '後端與 HTTPS 連接埠不能相同', '后端端口不能与 HTTPS 端口相同'),
}


# CLI terminology shared with the Web interface.
MESSAGES.update({'S2L 管理': ('S2L management', 'S2L 管理', 'S2L 管理'),
 '服务': ('Service', '服務', '服务'),
 '管理': ('Management', '管理', '管理'),
 '维护': ('Maintenance', '維護', '维护'),
 '管理地址': ('Management URL', '管理位址', '管理地址'),
 '服务状态': ('Service status', '服務狀態', '服务状态'),
 '重启服务': ('Restart services', '重啟服務', '重启服务'),
 '服务日志': ('Service logs', '服務日誌', '服务日志'),
 '启动服务': ('Start service', '啟動服務', '启动服务'),
 '停止服务': ('Stop service', '停止服務', '停止服务'),
 '管理密码': ('Management password', '管理密碼', '管理密码'),
 '管理用户名': ('Management username', '管理用戶名', '管理用户名'),
 '管理端口': ('Management port', '管理連接埠', '管理端口'),
 'SSH 端口': ('SSH port', 'SSH 連接埠', 'SSH 端口'),
 '软件更新': ('Software updates', '軟體更新', '软件更新'),
 '内核维护': ('Kernel maintenance', '核心維護', '内核维护'),
 '卸载 S2L': ('Uninstall S2L', '解除安裝 S2L', '卸载 S2L'),
 '退出': ('Exit', '結束', '退出'),
 '用法：s2l [0-14|kernel-upgrade|uninstall]；s2l uninstall --dry-run 只读预览': ('Usage: s2l [0-14|kernel-upgrade|uninstall]; '
                                                                         's2l uninstall --dry-run previews removal',
                                                                         '用法：s2l [0-14|kernel-upgrade|uninstall]；s2l '
                                                                         'uninstall --dry-run 僅預覽',
                                                                         '用法：s2l [0-14|kernel-upgrade|uninstall]；s2l '
                                                                         'uninstall --dry-run 只读预览'),
 '用法：s2l 打开菜单；s2l [0-14] 执行对应操作；s2l kernel-upgrade 准备内核（不自动重启）；s2l uninstall --dry-run 只读预览卸载。': ('Usage: s2l opens '
                                                                                                  'the menu; s2l '
                                                                                                  '[0-14] runs an '
                                                                                                  'action; s2l '
                                                                                                  'kernel-upgrade '
                                                                                                  'prepares the kernel '
                                                                                                  'without rebooting; '
                                                                                                  's2l uninstall '
                                                                                                  '--dry-run previews '
                                                                                                  'removal.',
                                                                                                  '用法：s2l 開啟選單；s2l '
                                                                                                  '[0-14] 執行對應操作；s2l '
                                                                                                  'kernel-upgrade '
                                                                                                  '準備核心（不自動重啟）；s2l '
                                                                                                  'uninstall --dry-run '
                                                                                                  '僅預覽解除安裝。',
                                                                                                  '用法：s2l 打开菜单；s2l '
                                                                                                  '[0-14] 执行对应操作；s2l '
                                                                                                  'kernel-upgrade '
                                                                                                  '准备内核（不自动重启）；s2l '
                                                                                                  'uninstall --dry-run '
                                                                                                  '只读预览卸载。'),
 '无效选项：': ('Invalid option: ', '無效選項：', '无效选项：'),
 '；使用 s2l --help 查看用法。': ('; see s2l --help.', '；使用 s2l --help 查看用法。', '；使用 s2l --help 查看用法。'),
 '请以 root 执行：sudo s2l': ('Run as root: sudo s2l', '請以 root 執行：sudo s2l', '请以 root 执行：sudo s2l'),
 '按回车返回菜单...': ('Press Enter to return to the menu...', '按 Enter 返回選單...', '按回车返回菜单...'),
 '未找到配置文件：': ('Configuration file not found: ', '找不到設定檔：', '未找到配置文件：'),
 '请选择操作 [0-14]: ': ('Select an action [0-14]: ', '請選擇操作 [0-14]：', '请选择操作 [0-14]: '),
 '保存后将重启服务，当前拨号连接会中断；留空取消。': ('Saving restarts the service and disconnects VPN clients. Leave blank to cancel.',
                              '儲存後會重啟服務並中斷 VPN 連線；留空取消。',
                              '保存后将重启服务，当前拨号连接会中断；留空取消。'),
 '新管理用户名: ': ('New management username: ', '新管理使用者名稱：', '新管理用户名: '),
 '新管理密码（12-128 位）: ': ('New management password (12-128 characters): ', '新管理密碼（12–128 字元）：', '新管理密码（12-128 位）: '),
 '再次输入密码: ': ('Confirm password: ', '再次輸入密碼：', '再次输入密码: '),
 '两次密码不一致，未修改。': ('Passwords do not match; nothing changed.', '兩次密碼不一致，未變更。', '两次密码不一致，未修改。'),
 '请先放行新 TCP 端口；HTTPS 部署将修改 Nginx 端口，保留现有证书。': ('Allow the new TCP port first. HTTPS changes the Nginx port and retains '
                                               'its certificate.',
                                               '請先開放新 TCP 連接埠；HTTPS 部署會修改 Nginx 連接埠並保留憑證。',
                                               '请先放行新 TCP 端口；HTTPS 部署将修改 Nginx 端口，保留现有证书。'),
 '新网页端口（1-65535）: ': ('New management port (1-65535): ', '新管理連接埠（1–65535）：', '新网页端口（1-65535）: '),
 '请先在安全组/防火墙放行新 SSH 端口，并保持当前连接。': ('Allow the new SSH port in the security group/firewall and keep this connection '
                                   'open.',
                                   '請先在安全群組／防火牆開放新 SSH 連接埠，並保留目前連線。',
                                   '请先在安全组/防火墙放行新 SSH 端口，并保持当前连接。'),
 '旧端口将停止接受新连接；修改后需在另一个终端验证登录。': ('The old port will stop accepting new connections. Verify login in another terminal.',
                                 '舊連接埠將停止接受新連線；修改後請在另一個終端驗證登入。',
                                 '旧端口将停止接受新连接；修改后需在另一个终端验证登录。'),
 '新 SSH 端口（1-65535，留空取消）: ': ('New SSH port (1-65535, blank to cancel): ',
                              '新 SSH 連接埠（1–65535，留空取消）：',
                              '新 SSH 端口（1-65535，留空取消）: '),
 '已放行新端口并准备验证？输入 yes 继续: ': ('Is the port allowed and a verification terminal ready? Type yes to continue: ',
                             '已開放新連接埠並準備驗證？輸入 yes 繼續：',
                             '已放行新端口并准备验证？输入 yes 继续: '),
 '已取消': ('Cancelled', '已取消', '已取消'),
 '无效选项': ('Invalid option', '無效選項', '无效选项'),
 'S2L 已重启': ('S2L restarted', 'S2L 已重啟', 'S2L 已重启'),
 'S2L 已启动': ('S2L started', 'S2L 已啟動', 'S2L 已启动'),
 'S2L 已停止': ('S2L stopped', 'S2L 已停止', 'S2L 已停止'),
 '管理监听地址格式错误': ('Invalid management listener address', '管理監聽位址格式錯誤', '管理监听地址格式错误'),
 '网页管理地址：{address}': ('Management URL: {address}', '網頁管理位址：{address}', '网页管理地址：{address}'),
 '未能查询公网地址，以上为本机出口地址；存在 NAT 时请使用实际公网 IP。': ('Public IP lookup failed; this is the local egress address. Use the actual '
                                            'public IP behind NAT.',
                                            '無法查詢公網位址，以上為本機出口位址；有 NAT 時請使用實際公網 IP。',
                                            '未能查询公网地址，以上为本机出口地址；存在 NAT 时请使用实际公网 IP。'),
 '无法自动识别服务器地址，请访问 http://服务器公网IP:{port}': ('Cannot detect the server address; visit http://SERVER_PUBLIC_IP:{port}',
                                           '無法自動識別伺服器位址，請開啟 http://伺服器公網IP:{port}',
                                           '无法自动识别服务器地址，请访问 http://服务器公网IP:{port}'),
 '管理服务仅在本机监听，请通过 SSH 隧道或反向代理访问。': ('The backend listens locally; use an SSH tunnel or reverse proxy.',
                                   '管理服務僅在本機監聽，請透過 SSH 隧道或反向代理存取。',
                                   '管理服务仅在本机监听，请通过 SSH 隧道或反向代理访问。'),
 '证书类型：自签证书，浏览器会显示信任警告；证书位于 /opt/s2l/tls/self-signed.crt。': ('Certificate: self-signed; browsers show a trust warning. '
                                                             'File: /opt/s2l/tls/self-signed.crt.',
                                                             '憑證：自簽憑證，瀏覽器會顯示信任警告；檔案位於 /opt/s2l/tls/self-signed.crt。',
                                                             '证书类型：自签证书，浏览器会显示信任警告；证书位于 /opt/s2l/tls/self-signed.crt。'),
 '管理监听地址：{listen}': ('Backend listener: {listen}', '管理監聽位址：{listen}', '管理监听地址：{listen}'),
 '管理用户名：{user}': ('Management username: {user}', '管理使用者名稱：{user}', '管理用户名：{user}'),
 '未启用认证': ('Authentication disabled', '未啟用驗證', '未启用认证'),
 '管理密码：不在命令输出中显示。': ('Management password: not shown.', '管理密碼：不會顯示於命令輸出。', '管理密码：不在命令输出中显示。'),
 '读取配置失败：{error}': ('Cannot read configuration: {error}', '讀取設定失敗：{error}', '读取配置失败：{error}'),
 '配置错误：{error}': ('Configuration error: {error}', '設定錯誤：{error}', '配置错误：{error}'),
 '设置已保存，S2L 已重启': ('Settings saved; S2L restarted', '設定已儲存，S2L 已重啟', '设置已保存，S2L 已重启'),
 '配置必须是 JSON 对象': ('Configuration must be a JSON object', '設定必須是 JSON 物件', '配置必须是 JSON 对象'),
 '请先在配置文件中设置管理用户名和至少 12 位的密码': ('Configure a management username and a password of at least 12 characters first',
                                '請先在設定檔中指定管理使用者名稱及至少 12 字元的密碼',
                                '请先在配置文件中设置管理用户名和至少 12 位的密码'),
 '不支持符号链接形式的 Nginx 配置': ('Symlinked Nginx configuration is unsupported', '不支援符號連結形式的 Nginx 設定', '不支持符号链接形式的 Nginx 配置'),
 '仅支持 S2L 管理的 Nginx HTTPS 配置': ('Only S2L-managed Nginx HTTPS configuration is supported',
                                '僅支援 S2L 管理的 Nginx HTTPS 設定',
                                '仅支持 S2L 管理的 Nginx HTTPS 配置'),
 'Nginx 配置与管理地址不一致，未修改': ('Nginx configuration and management URL differ; nothing changed',
                          'Nginx 設定與管理位址不一致，未變更',
                          'Nginx 配置与管理地址不一致，未修改'),
 '缺少配置项': ('Missing configuration field', '缺少設定項目', '缺少配置项'),
 '用户名需为 1-64 位字母、数字、点、下划线或连字符': ('Username must be 1-64 letters, digits, dots, underscores or hyphens',
                                 '使用者名稱須為 1–64 字元，可含字母、數字、點、底線或連字號',
                                 '用户名需为 1-64 位字母、数字、点、下划线或连字符'),
 'HTTPS 端口修改失败，已恢复原配置': ('HTTPS port change failed; original configuration restored',
                         'HTTPS 連接埠修改失敗，已還原原始設定',
                         'HTTPS 端口修改失败，已恢复原配置'),
 '网页端口未改变': ('Management port unchanged', '管理連接埠未變更', '网页端口未改变'),
 '新配置启动失败，已恢复原配置和服务': ('Startup with the new settings failed; original settings and service restored',
                       '新設定啟動失敗，已還原原始設定及服務',
                       '新配置启动失败，已恢复原配置和服务'),
 '密码需为 12-128 位字母、数字或 _ . @ + -': ('Password must be 12-128 letters, digits or _ . @ + -',
                                   '密碼須為 12–128 字元，可含字母、數字或 _ . @ + -',
                                   '密码需为 12-128 位字母、数字或 _ . @ + -'),
 '不支持的配置项': ('Unsupported configuration field', '不支援的設定項目', '不支持的配置项'),
 '原监听地址格式错误，请检查配置': ('Invalid existing listener address; check the configuration',
                     '原監聽位址格式錯誤，請檢查設定',
                     '原监听地址格式错误，请检查配置'),
 '已恢复原配置，但 Nginx 重载失败，请检查服务日志': ('Original configuration restored, but Nginx reload failed; check service logs',
                                 '已還原原始設定，但 Nginx 重新載入失敗，請檢查服務日誌',
                                 '已恢复原配置，但 Nginx 重载失败，请检查服务日志'),
 '已恢复原配置，但服务启动失败；请查看 journalctl -u s2l': ('Original settings restored, but startup failed; check journalctl -u s2l',
                                          '已還原原始設定，但服務啟動失敗；請檢查 journalctl -u s2l',
                                          '已恢复原配置，但服务启动失败；请查看 journalctl -u s2l'),
 'HTTPS 管理地址格式错误': ('Invalid HTTPS management URL', 'HTTPS 管理位址格式錯誤', 'HTTPS 管理地址格式错误'),
 '新 HTTPS 端口未正常监听': ('The new HTTPS port is not listening', '新 HTTPS 連接埠未正常監聽', '新 HTTPS 端口未正常监听'),
 'SSH 配置包含链接或 /etc/ssh 外的文件，请手动修改': ('SSH configuration includes symlinks or files outside /etc/ssh; edit manually',
                                     'SSH 設定包含連結或 /etc/ssh 外的檔案，請手動修改',
                                     'SSH 配置包含链接或 /etc/ssh 外的文件，请手动修改'),
 '未能读取 SSH 监听地址': ('Cannot read SSH listen addresses', '無法讀取 SSH 監聽位址', '未能读取 SSH 监听地址'),
 'SSH 端口未改变': ('SSH port unchanged', 'SSH 連接埠未變更', 'SSH 端口未改变'),
 'SSH 服务未运行，请先启动 OpenSSH 服务': ('SSH is not running; start OpenSSH first',
                               'SSH 服務未執行，請先啟動 OpenSSH',
                               'SSH 服务未运行，请先启动 OpenSSH 服务'),
 'SSH 服务 KillMode 不是 process，自动修改可能中断当前连接，请手动修改': ('SSH KillMode is not process; an automatic change may disconnect '
                                                   'this session. Edit manually.',
                                                   'SSH KillMode 不是 process，自動修改可能中斷目前連線，請手動修改',
                                                   'SSH 服务 KillMode 不是 process，自动修改可能中断当前连接，请手动修改'),
 '新 SSH 端口未正常监听': ('The new SSH port is not listening', '新 SSH 連接埠未正常監聽', '新 SSH 端口未正常监听'),
 'SSH socket 配置为链接，请手动修改': ('SSH socket configuration is a symlink; edit manually',
                            'SSH socket 設定為連結，請手動修改',
                            'SSH socket 配置为链接，请手动修改'),
 'SSH 有其他端口配置，自动修改已取消': ('Other SSH port settings were found; automatic change cancelled',
                         'SSH 有其他連接埠設定，已取消自動修改',
                         'SSH 有其他端口配置，自动修改已取消'),
 'ListenAddress 显式指定了其他端口，请手动修改': ('ListenAddress specifies another port; edit manually',
                                   'ListenAddress 明確指定其他連接埠，請手動修改',
                                   'ListenAddress 显式指定了其他端口，请手动修改'),
 '修改失败，已恢复原 SSH 配置': ('Change failed; original SSH configuration restored', '修改失敗，已還原原始 SSH 設定', '修改失败，已恢复原 SSH 配置'),
 'SSH 端口修改失败：{error}': ('SSH port change failed: {error}', 'SSH 連接埠修改失敗：{error}', 'SSH 端口修改失败：{error}'),
 'SSH 端口已改为 {port}；请在新终端使用 ssh -p {port} 用户名@服务器IP 验证登录': ('SSH port changed to {port}; verify in another terminal: '
                                                           'ssh -p {port} USER@SERVER_IP',
                                                           'SSH 連接埠已改為 {port}；請在另一個終端驗證：ssh -p {port} 使用者@伺服器IP',
                                                           'SSH 端口已改为 {port}；请在新终端使用 ssh -p {port} 用户名@服务器IP 验证登录'),
 '配置备份：{path}；确认新连接成功前不要关闭当前终端': ('Configuration backup: {path}; keep this terminal open until the new login succeeds',
                                  '設定備份：{path}；確認新連線成功前請勿關閉目前終端',
                                  '配置备份：{path}；确认新连接成功前不要关闭当前终端'),
 '原配置已恢复，但 SSH 重载失败；请通过服务器控制台检查，备份：{path}': ('Original configuration restored, but SSH reload failed; use the server '
                                             'console. Backup: {path}',
                                             '已還原原始設定，但 SSH 重新載入失敗；請透過伺服器控制台檢查。備份：{path}',
                                             '原配置已恢复，但 SSH 重载失败；请通过服务器控制台检查，备份：{path}')})


MESSAGES.update({'语言': ('Language', '語言', '语言'),
 '语言无效，请选择 en、zh-Hant 或 zh-Hans': ('Invalid language; choose en, zh-Hant or zh-Hans',
                               '語言無效，請選擇 en、zh-Hant 或 zh-Hans',
                               '语言无效，请选择 en、zh-Hant 或 zh-Hans'),
 '语言设置保存失败，请检查本机面板服务。': ('Cannot save language; check the local Web service.',
                         '無法儲存語言，請檢查本機網頁服務。',
                         '语言设置保存失败，请检查本机面板服务。'),
 '语言设置已保存，Web 刷新后同步。': ('Language saved; refresh the Web interface to synchronize.',
                        '語言已儲存，重新整理網頁後同步。',
                        '语言设置已保存，Web 刷新后同步。'),
 '选择语言 [1-3，0 取消]: ': ('Select a language [1-3, 0 to cancel]: ', '選擇語言 [1–3，0 取消]：', '选择语言 [1-3，0 取消]: ')})


MESSAGES['语言切换需要本机管理服务监听。'] = ('Language changes require a local management listener.', '切換語言需要本機管理服務監聽。', '语言切换需要本机管理服务监听。')


def tr(message):
    language = os.environ.get('_S2L_INSTALL_LANG')
    if language not in ('en', 'zh-Hant', 'zh-Hans'):
        return message
    translations = MESSAGES.get(message)
    return translations[('en', 'zh-Hant', 'zh-Hans').index(language)] if translations else message


def cli_catalog(config_path):
    """Read only the saved Web language; never serialize configuration secrets."""
    import json
    from pathlib import Path
    language = 'en'
    try:
        config = json.loads(Path(config_path).read_text(encoding='utf-8'))
        value = config.get('ui_language') if isinstance(config, dict) else None
        if isinstance(value, str):
            language = {'en': 'en', 'zh-TW': 'zh-Hant', 'zh-CN': 'zh-Hans'}.get(value, 'en')
    except (OSError, ValueError):
        pass
    index = ('en', 'zh-Hant', 'zh-Hans').index(language)
    return {'_language': language, **{key: values[index] for key, values in MESSAGES.items()}}


if __name__ == '__main__':
    import sys
    if len(sys.argv) != 3 or sys.argv[1] != '--cli':
        raise SystemExit('Usage: installer_i18n.py --cli CONFIG')
    # NUL framing is data, not shell code: translations and configuration are
    # never evaluated by bash, including quotes and control characters.
    for key, value in cli_catalog(sys.argv[2]).items():
        sys.stdout.buffer.write((key + '\0' + value + '\0').encode('utf-8'))

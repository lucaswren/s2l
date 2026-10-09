# s2l

将 SOCKS5 节点转换为 L2TP 出口线路。推荐 **Ubuntu Server 24.04 LTS（amd64）**，安装脚本支持 Ubuntu/Debian 的 amd64、arm64。

## 关闭 Ubuntu 自动更新

以下命令关闭 APT 自动更新，仍可手动执行 `apt update && apt upgrade`。请定期安装安全更新。

```bash
sudo systemctl disable --now apt-daily.timer apt-daily-upgrade.timer
sudo systemctl stop unattended-upgrades.service
sudo tee /etc/apt/apt.conf.d/20auto-upgrades >/dev/null <<'EOF'
APT::Periodic::Update-Package-Lists "0";
APT::Periodic::Unattended-Upgrade "0";
EOF
```

恢复自动更新：

```bash
sudo tee /etc/apt/apt.conf.d/20auto-upgrades >/dev/null <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
EOF
sudo systemctl enable --now apt-daily.timer apt-daily-upgrade.timer
```

## 安装与管理

安装：

```bash
curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_install.sh | sudo bash
```

新服务器安装会**自动启用 HTTPS**，优先申请可信公网 IP 证书并配置自动续期；申请失败自动使用自签证书。安装前放行 TCP **80** 和 HTTPS 管理端口；成功后显示 `https://服务器IP:端口`、管理账号与 **16 位随机密码**。HTTPS 管理端口和本机后端端口分别随机选择 **10000-60000** 范围内的空闲端口，SSH 端口保留现有设置；自定义密码至少 **12 位**。

公网 IPv4 默认自动识别；需指定时，将安装命令末尾的 `sudo bash` 替换为 `sudo env PUBLIC_IP=服务器公网IPv4 bash`。`LISTEN_PORT` 仅指定本机后端端口。使用 `HTTPS_PORT` 指定 HTTPS 管理端口，例如：

```bash
curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_install.sh | sudo env HTTPS_PORT=8443 bash
```

请提前放行 TCP 80、8443；不指定时，安装过程显示随机 HTTPS 端口，需放行后才能访问。

1. 使用安装时显示的 HTTPS 地址登录。
2. 添加 SOCKS5 节点，创建映射，启动至 `running`。
3. 客户端选择 L2TP，填写服务器 IP 和映射账号密码；默认不启用 IPsec。

节点列表点击“上下行测速”，分别显示下载、上传速度与连接延迟。测速由服务器经所选 SOCKS5 节点访问测速站，默认每个方向传输 5 MB；单向失败会保留另一个方向的结果。

网页“系统设置”可修改管理用户名、密码、HTTPS 端口和 SSH 端口，登录后无需再次输入管理密码。新密码至少 12 位，留空则只修改用户名；账号修改立即生效，无需重启服务。修改 SSH 前先放行新 TCP 端口，成功后用 `ssh -p 新端口 用户名@服务器IP` 验证登录。

网页“系统设置 → SSH 登录密码”可修改 root 的 Linux 登录密码，仅允许已登录管理员通过 HTTPS 操作，确认新密码即可，无需再次输入管理密码。支持生成 16 位随机密码，手动密码至少 12 位（最多 128 位可见 ASCII 字符）。修改后保留原 SSH 连接，在新终端验证登录；此操作不启用密码登录、不解除账号锁定，也不修改网页管理密码。

网页“系统设置”可启用 **L2TP/IPsec（IKEv1 + PSK）**，适用于支持该模式的 L2TP 客户端。密钥至少 12 位，所有映射共用；留空保留现有密钥，列表不会返回密钥。启用前放行 UDP 500、4500 与 ESP（IP 协议 50），客户端填写相同 PSK；开启后服务器阻止普通 L2TP 接入。关闭后客户端清空 PSK。切换模式或更换密钥需重新拨号。配置和防火墙失败会回滚，备份为 `/opt/s2l/ipsec-backup.json`。与已有 IPsec 服务冲突时会拒绝修改。具体兼容性需按客户端版本验证。

普通 L2TP 需在安全组放行 UDP 1701。管理页面能修改网络并查看凭据，请限制HTTPS 管理端口的来源 IP。

配置：`/opt/s2l/config.json`；运行数据：`/opt/s2l/data/state.json`。数据文件含代理和拨号凭据，请限制访问权限。

## 网页 HTTPS（公网 IP）

新安装自动完成以下步骤。已有 HTTP 安装可在放行 TCP 80 和指定 HTTPS 端口后，于项目目录执行：

```bash
sudo bash script/setup_https.sh 服务器公网IPv4 8443
s2l 1
```

使用 Let's Encrypt 公网 IP 证书和 Nginx；申请成功后将后端限制为本机监听，管理地址变为 `https://服务器IP:8443`。可信证书申请失败时自动回退到有效期 365 天、包含服务器 IP 的自签证书，继续启用 HTTPS；已有有效可信证书会优先保留。自签证书会触发浏览器信任警告，安装输出显示 SHA-256 指纹，请核对后手动信任或导入证书。自签证书不配置 Certbot 自动续期；修复申请条件后重新执行上述命令，可切换回可信证书。其他部署错误会报错并恢复原配置。安装脚本拒绝覆盖已有配置。IP 证书有效期约 6 天，由 `s2l-cert-renew.timer` 每日检查两次并自动续期，TCP 80 须保持可达。HTTPS 端口可通过网页“系统设置 → HTTPS 管理端口”或 `s2l 9` 修改；保留本机后端端口与证书，更新管理 URL 和 HTTP 跳转，Nginx 配置校验或重载失败自动回滚。

## 管理命令

服务器终端执行：

```bash
s2l
```

也可直接执行编号，完成后退出，不返回菜单：

```bash
s2l 1   # 显示完整管理 URL 和用户名，不输出密码
s2l 2   # 查看服务状态
s2l 4   # 查看最近日志
s2l 7   # 按提示修改管理密码
s2l 10  # 按提示修改 SSH 端口
```

地址优先使用配置中的 `public_url`（适用于域名或 HTTPS 反向代理）；否则根据监听地址和公网 IP 生成。公网查询失败时显示本机出口地址并提示 NAT 情况。

普通用户会自动请求 `sudo` 权限。输入编号并按回车操作：

| 编号 | 操作 |
|---|---|
| 1 | 查看网页管理地址 |
| 2 | 查看服务状态 |
| 3 | 重启服务 |
| 4 | 查看最近 80 条日志 |
| 5 | 启动服务 |
| 6 | 停止服务 |
| 7 | 修改管理密码（12-128 位，输入隐藏，需再次确认） |
| 8 | 修改管理用户名 |
| 9 | 修改网页管理端口（1-65535） |
| 10 | 修改 SSH 端口（1-65535） |
| 0 | 退出菜单 |

例如修改密码：执行 `s2l` → 输入 `7` → 输入两次新密码。操作后按回车返回菜单，输入 `0` 退出。

修改管理账号、密码或网页端口时留空取消；管理账号或 HTTP 后端端口变更会重启 s2l，短暂中断拨号连接，启动失败恢复原配置；HTTPS 端口变更只重载 Nginx。用户名允许字母、数字、`.`、`_`、`-`，密码还允许 `@`、`+`。修改账号密码后重新登录；先放行新网页端口，再修改并使用新端口访问。

修改 SSH：先放行新 TCP 端口，执行 `s2l` → 输入 `10` → 输入端口 → 输入 `yes`。脚本备份 SSH 配置，执行 `sshd -t` 并检查新端口监听，失败自动恢复；兼容普通 SSH 服务和 Ubuntu `ssh.socket`。新端口应用后，在另一终端执行 `ssh -p 新端口 用户名@服务器IP` 验证成功，再关闭原连接。备份位于 `/opt/s2l/ssh-port-backup.json`。

命令路径：`/usr/local/bin/s2l`；配置助手：`/opt/s2l/manage_config.py`。

## 项目结构与构建

| 目录 | 用途 |
|---|---|
| `cmd/s2l/` | Go 程序入口 |
| `internal/` | API、配置、存储、网络、L2TP 和 SOCKS5 管理 |
| `web/src/` | Vue 3 + Element Plus 前端源码 |
| `web/dist/` | 嵌入 Go 程序的前端构建资源，需提交 |
| `script/` | 安装、HTTPS 和系统管理脚本 |
| `deploy/systemd/` | 服务与证书续期任务 |

修改前端后先构建资源，再编译 Go 程序：

```bash
npm --prefix web ci
npm --prefix web run build
go build -o bin/s2l ./cmd/s2l
```

开发时使用 `npm --prefix web run dev`；API 默认代理到本机 `8080` 端口。
运行配置和数据不提交；本地依赖、IDE 配置与编译产物由 `.gitignore` 排除。

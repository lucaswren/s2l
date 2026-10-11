# S2L

[English（主要文档）](README.md) · [繁體中文](README.zh-Hant.md) · 简体中文

S2L 在 Linux 上管理 L2TP 客户端连接，通过 sing-box、策略路由与 iptables 将流量导向指定 SOCKS5 上游。Go 服务内嵌 Vue 3 / Element Plus 管理界面，支持节点与映射管理、批量导入、启停、重启恢复、上下行测速与系统设置。界面及安装消息目前主要为简体中文。

仅在获得授权的主机、网络与上游服务上使用。部署前阅读[法律与合规说明](docs/zh-Hans/legal.md)，尤其涉及中国大陆或跨境连接时。软件不提供监管许可，也不保证匿名性。

## 安装与安全

安装程序支持 Ubuntu/Debian 的 amd64、arm64，建议基准为 Ubuntu Server 24.04 LTS amd64。隧道需要 Linux、systemd、root 权限与 TUN；非 Linux 构建无法提供隧道服务。

**默认普通 L2TP 不加密。** 管理 HTTPS 不会加密隧道流量。可选 IPsec 使用 IKEv1 与共享 PSK，须验证客户端兼容性；SOCKS5 本身也不加密。UDP 能力取决于上游，路由主要为 IPv4，没有防泄漏保证。

使用专用主机并保留安全更新。安装会修改软件包、转发、PPP/L2TP、防火墙及系统服务。执行前审阅脚本，正式部署宜固定至已审阅的提交。

```bash
git clone https://github.com/lucaswren/s2l.git
cd s2l
# 先审阅 script/install.sh 与相关辅助脚本。
sudo env HTTPS_PORT=8443 bash script/install.sh
```

仅允许可信管理 IP 访问 TCP 8443。安装生成 365 天自签证书与 16 字符随机管理密码，后端监听 `127.0.0.1:8080`。信任证书前通过可信渠道核对 SHA-256 指纹。省略 `HTTPS_PORT` 时选用 10000–60000 空闲端口，不得使用 8080；必要时以 `PUBLIC_IP` 指定公网 IPv4。

登录显示的 HTTPS 地址，添加授权节点，创建映射并启动至 `running`。客户端使用服务器 IP 与映射账号密码。普通 L2TP 使用 UDP 1701；IPsec 需要 UDP 500/4500 及 ESP（IP 协议 50）。先阅读操作指南再开放端口。更新现有安装不要重跑安装程序。

## 文档

- [操作、备份、更新与故障排查](docs/zh-Hans/operations.md)
- [架构、配置与开发](docs/zh-Hans/development.md)
- [法律与合规](docs/zh-Hans/legal.md)
- [安全问题报告（英文）](SECURITY.md)

需要 Go 1.22 以上及兼容 Node.js/npm。依次执行 `npm --prefix web ci`、`npm --prefix web run build`、`go build -o bin/s2l ./cmd/s2l`。前端变更须提交 `web/dist/`。

S2L 原创代码与文档采用 [Apache-2.0](LICENSE)。参阅 [NOTICE](NOTICE) 与[第三方声明](THIRD_PARTY_NOTICES.md)，依赖组件保留各自条款。软件许可不代表取得监管许可。

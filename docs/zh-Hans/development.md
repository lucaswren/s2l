# 架构、配置与开发

[首页](../../README.zh-Hans.md) · [English](../en/development.md) · [繁體中文](../zh-Hant/development.md) · [简体中文](../zh-Hans/development.md)

[客户端接入、DNS 与验收](clients.md)

L2TP/PPP 客户端连接 xl2tpd；S2L 将映射账号密码与 IPv4 子网关联至 sing-box TUN、策略路由表及防火墙规则，再由 sing-box 连接 SOCKS5 上游。管理界面的 Nginx HTTPS 代理与此数据路径分开。

`cmd/s2l/` 负责启动与关闭；`internal/api/` 提供网页与脚本会话鉴权及管理 API；`internal/service/` 编排映射生命周期。`internal/singbox/`、`network/`、`l2tp/` 负责进程、路由、防火墙与 PPP；`store/`、`model/`、`config/` 处理状态、模型与配置。`web/src/` 是前端源码，`web/dist/` 是须提交的内嵌资源；`script/` 与 `deploy/systemd/` 管理安装及服务。

`internal/dnsproxy/` 负责每条映射的 DoH/TCP；`internal/diagnostics/` 与 `speedtest/` 负责分阶段检测；`history/`、`operations/`、`debuglog/` 分别保留有界历史、事件通知与临时日志。`web/src/i18n/` 集中存放三语言文案，页面复用 `components/` 与 `utils/`。

源码发布须保留 `web/dist/`、依赖锁定文件、第三方许可证声明与各平台 Go 实现。`bin/`、`tmp/`、`.test-tmp/`、`web/node_modules/` 及 Python 缓存属于本地产物，清理前先结束相关测试进程，保留仍需分析的诊断资料。浏览器测试共用 `script/ui_test_helpers.mjs`，每次结束后停止自己的子进程并删除临时配置与后端数据，截图则保留在 `tmp/`。

## 配置

未指定 `-config` 时使用默认配置；明确指定的文件必须存在且内容为 JSON 对象。文件不存在或内容为 `null` 时会停止启动，不会悄悄改用默认值。

以 `config.example.json` 为起点，在合适测试主机使用 `./bin/s2l -config /path/to/config.json`。`listen` 默认 `127.0.0.1:8080`；`admin_user` 与 `admin_pass` 须一起配置，密码至少 12 字符。示例账号密码为空，只允许本机监听，但反向代理仍可能将未验证的服务公开，代理前必须设置账号密码。

`singbox_bin` 程序默认为 `sing-box`，示例为 `/usr/local/bin/sing-box`；`data_dir` 默认 `./data`，相对于工作目录，含 `state.json`。安装服务工作目录为 `/opt/s2l`。`chap_secrets`、`xl2tpd_conf`、`ppp_options` 分别默认 `/etc/ppp/chap-secrets`、`/etc/xl2tpd/xl2tpd.conf`、`/etc/ppp/options.xl2tpd`。`auto_restore` 默认 `true`，恢复已保存为 `running` 的映射。脚本管理的 `public_url` 等元数据不属于 Go `Config` 字段。

## 从已审阅源码安装

文档优先介绍一键安装。若需安装指定的已审阅版本，请将 `YOUR_REVIEWED_COMMIT` 替换为该提交 ID，并在新主机执行以下命令；执行最后一步前，审阅安装脚本与相关管理脚本。

```bash
git clone https://github.com/lucaswren/s2l.git
cd s2l
git checkout YOUR_REVIEWED_COMMIT
sudo env HTTPS_PORT=8443 bash script/install.sh
```

## 构建与贡献

```bash
npm --prefix web ci
npm --prefix web run build
go build -o bin/s2l ./cmd/s2l
go test ./...
python3 -m unittest discover -s script/tests
```

`make build` 包含前端构建；`make test` 执行 Go 测试。`npm --prefix web run dev` 的 API 代理至本机 8080。非 Linux 测试不验证实际 Linux 隧道，集成验证须使用授权、可丢弃的 Linux VM。`make run` 启动本机后端，可能修改主机网络与 PPP 文件。`make install-service` 统一调用 `script/install.sh`，包括依赖、HTTPS 与服务配置，仅在预定全新安装的主机执行；已有安装使用 `script/update.sh`。

变更须说明触发条件、结果与相关验证；勿提交运行配置、状态、依赖或秘密。前端变更须重建并提交 `web/dist/`。文档先更新英文，再繁体、简体中文，保持命令、默认值与法律语义一致。法律来源使用官方链接并记录核查日期；仅提交有权按 Apache-2.0 许可的内容，保留第三方声明。依赖更新后执行 `python3 script/collect_frontend_licenses.py` 并提交声明清单。

文档、界面与服务描述的展示名称统一为 **S2L**；可执行文件、模块与仓库路径、命令、配置键及现有程序识别标记保留小写 `s2l`。协议名称统一为 SOCKS5、L2TP、IPsec；须区分服务器端测速与客户端完整路径，以及尝试回滚与已验证的恢复结果。

中文文档按文字体系使用 `zh-Hant`（繁體中文）与 `zh-Hans`（简体中文），遵循 [BCP 47 文字子标签](https://www.rfc-editor.org/rfc/rfc5646.html#section-2.2.3)。语言标签表示文字体系，法律适用地区另在内容中说明。文件名、导航链接与 HTML 语言属性须保持一致；提交前执行 `python3 script/check_docs.py` 检查本地文档链接。


## 诊断集成测试

```bash
# 先构建 web/dist；需要 Chrome、Node.js 22+ 和 Python 3。
node script/test_ui.mjs
node script/test_auth_ui.mjs

# 仅在隔离 Linux 测试主机运行，需内核 PPP/L2TP 支持及测试依赖。
go test -c -o /tmp/s2l-l2tp.test ./internal/service
sudo python3 script/test_l2tp_integration.py --test-binary /tmp/s2l-l2tp.test --singbox /absolute/path/to/sing-box
for transport in ipsec ipsec-nat ipsec-shared-nat ipsec-shared-nat-1701; do
  sudo python3 script/test_l2tp_integration.py --test-binary /tmp/s2l-l2tp.test --singbox /absolute/path/to/sing-box --transport "$transport"
done
```

L2TP 测试隔离挂载、网络与 PID 命名空间，通过真实 PPP IPCP 验证 DNS 下发，检查自定义 DoH、同节点 TCP DNS 回退及客户端 UDP/TCP DNS 查询，并验证双固定出口。单个及双上游中断时，HTTPS 与未缓存 DNS 请求须失败，PPP 保持连接。另设可直接到达且证书验证成功的 HTTPS 出口作正向对照；故障与 TUN 消失期间，其出站数据包计数须保持零。也检查 UDP 主动拒绝、进程恢复与规则修复。浏览器测试覆盖桌面、390/320 像素、语言切换与未保存表单；鉴权测试使用真实后端与临时数据，覆盖登录、会话保持及退出。页面测试不能替代 Linux 数据路径测试。

额外模式需要 strongSwan/starter、conntrack 以及内核 XFRM、连接跟踪区和 NAT INPUT 支持。`ipsec` 验证无 NAT 路由器的客户端（仍强制 NAT-T）；`ipsec-nat` 使用不同转换后地址；`ipsec-shared-nat` 共享公网地址、源端口分别为 1701/1702；`ipsec-shared-nat-1701` 同时共享公网地址及源端口 1701。均使用生产配置和 updown 辅助程序，验证 DNS、各节点固定出口、UDP allow/reject-quic、上游/TUN 故障及直连出站计数为零。相同端口模式还延迟 updown 复现早到包、在线修复防火墙并删除回包标记、轮换 CHILD_SA，并让一个客户端断线重拨，核对另一客户端不受影响、退役连接跟踪区为空。入站和出站防护缺失必须被检测并修复。外部节点探测单独运行；Linux 对端通过不代表原生 Windows/macOS 已验证。

相同端口测试还将分配计数置为 65,536，执行四轮端点回收，共新增七次真实重拨，其中三轮为双客户端并发。保留真实的 30 秒隔离期，由生产 updown 自动完成回收。残留 UDP 套接字或控制查询失败必须阻止复用，旧端点回包必须被出站防护拦截；单客户端回收期间持续探测另一个客户端的固定出口。每轮复核 DNS、UDP 策略、SOCKS 身份及 ESP，随后继续执行上游/TUN 中断和直连计数为零的故障测试。Python 回归覆盖 16 个并发进程分配新旧端点、45,540 次模拟生命周期、不完整/变化的全局快照、半开隧道、可恢复的中断日志及查询失败。长循环中的内核观察使用模拟，不代表进行了 45,540 次真实 VPN 拨号。

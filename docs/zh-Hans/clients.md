# 客户端接入与 DNS

[首页](../../README.zh-Hans.md) · [English](../en/clients.md) · [繁體中文](../zh-Hant/clients.md) · [简体中文](../zh-Hans/clients.md)

## 拨号前准备

启动映射，记录服务器地址、L2TP 账号密码及系统设置中的接入模式。启用 IPsec 时选择 L2TP/IPsec 并填写相同 PSK；所有映射共用此设置。普通 L2TP 不加密，并非所有原生客户端都支持，需核对系统及版本。一条映射只允许一条 PPP 会话；每个独立拨号设备使用不同映射及账号。多台 LAN 设备可共享路由器的一条会话。

服务器需要客户端可达的 IPv4。安装及 HTTPS 辅助脚本要求公网 IPv4；自动获取公网地址不等于外部可接入。服务器位于 NAT 后需配置入站转发及防火墙。无入站转发能力的 CGNAT 不适合直接远程拨号。

| 路径 | 要求 |
|---|---|
| 客户端 → 普通 L2TP 服务器 | UDP 1701 |
| 客户端 → IPsec 服务器 | UDP 500/4500；NAT-T |
| 服务器 → 固定 SOCKS5 | TCP 可达、凭据有效且支持 CONNECT；仅允许的 UDP 业务需要 UDP ASSOCIATE 与可达的中继 |
| SOCKS5 → 解析器及业务目标 | 可访问配置的 DoH HTTPS、TCP DNS 及应用目标 |

主机防火墙、云安全组、上游 NAT 和客户端网络都需允许所选模式。能打开 HTTPS 面板不代表能拨号。IPsec 启用后会阻止未保护的 L2TP，不要开放裸 UDP 1701 作为替代。参见 [strongSwan NAT-T 说明](https://docs.strongswan.org/docs/latest/features/natTraversal.html)。


S2L 现已在服务器端隔离同一 NAT 公网地址下全部使用 L2TP 源端口 1701 的客户端：每个 IPsec 安全关联使用独立标记、连接跟踪区和内部对端端口，客户端端口与固定 SOCKS5 映射不变。当前配置统一启用 NAT-T（UDP 500/4500），无 NAT 客户端也使用 UDP 封装。Linux 真实拨号覆盖两个 1701 客户端同时接入、密钥轮换、重新拨号、DNS、UDP 和上游中断。原生 Windows/macOS/路由器客户端以及服务器本身位于 NAT 后的部署仍需分别实测，不能据此宣称兼容所有客户端。

SOCKS5 UDP 仅放行代理的 **TCP 服务端口不够**。S2L 服务器还需双向到达 `UDP ASSOCIATE` 返回的 UDP 中继地址与端口，它可能是不同于 TCP 端口的动态端口；代理自身的出站 UDP 也需可用，并保持关联的 TCP 控制连接。协商成功不等于 UDP 转发成功。内置检测验证真实 UDP DNS 回包，不能证明所有 UDP 应用或 QUIC 正常。`reject-quic` 实际拒绝全部 UDP/443，并非识别 QUIC 协议；TCP MSS 不会改变 UDP 数据报大小。

如果 TCP/DoH 正常、UDP ASSOCIATE 成功却收不到数据报，应使用同一节点在另一个获准的网络接口上对照测试。本机 VPN/TUN、策略路由或上游防火墙都可能丢弃中继流量。TCP 控制连接和 UDP 必须使用同一出口接口，仅绑定 TCP 不够。另一路径成功能排除“节点完全不支持 UDP”，但不足以定位失败路径中的具体丢包设备。不得通过让客户端回退直连来绕过中继故障。

## 下发 DNS 与实际解析器

PPP 在 IPCP 协商时下发 **1.1.1.1 和 8.8.8.8**。客户端接受 VPN 自动 DNS。旧部署按[更新流程](operations.md)升级后，重新拨号获取 DNS；已有会话不会收到新的 PPP 选项。不要手动修改生成的 PPP 配置文件。

这两个地址是隧道内 DNS 请求的入口标识。进入映射 TUN 的 TCP/UDP 53 请求均被接管，实际解析经映射的固定 SOCKS5。自定义解析器不改变下发地址；无需公开 DNS 监听或开放 PPP 访问主机。如果 S2L 主机本身绑定了上述地址，应在客户端改用其他非本机 DNS 目的地址并路由进隧道：本机 INPUT 会在 TUN 接管前被阻断。

停止映射，在“编辑 → 高级选项”设置，保存后启动：

| 界面 / JSON 字段 | 默认值 | 格式 |
|---|---|---|
| DNS 模式 / `dns_mode` | `auto` | `auto`、`doh`、`tcp` |
| DoH 地址 / `dns_doh_url` | `https://1.1.1.1/dns-query` | HTTPS URL，可含端口与路径；不可含账号密码、查询参数、片段或空白 |
| TCP DNS 地址 / `dns_tcp_address` | `8.8.8.8:53` | `主机:端口` 或 `[IPv6]:端口`，1–65535；不支持带接口范围的 IPv6 |

留空恢复默认值；旧状态文件缺少字段时仍用相同默认值。解析器域名由 SOCKS5 远端解析。SOCKS5 服务器自身的域名仍可能使用 S2L 主机解析器，填写上游 IP 可避免这次启动解析。IPv6 解析器需要 SOCKS5 支持，不代表开启 PPP IPv6 转发。

映射 JSON 示例片段：

```json
{
  "dns_mode": "tcp",
  "dns_doh_url": "",
  "dns_tcp_address": "10.20.0.53:53"
}
```

这是完整映射的一部分，不能单独导入。私有 DNS 必须能**从 SOCKS5 出口到达**。请选择能回答所需公网及私有域名的解析器；尚无按域名分流的 DNS 规则。自动模式的两个端点应提供一致的解析范围；私有名称不应回退到默认公共 DNS 时，配置两个端点或使用强制模式。

自动模式在 DoH 查询失败时尝试 TCP，主解析连续失败后优先 TCP，恢复检查可切回 DoH；不切换 SOCKS5 节点、不改成服务器直连。强制模式只用指定传输。DNS 正常工作不要求 SOCKS5 支持 UDP，有效 NXDOMAIN 不触发切换。修改端点后映射解析器及 sing-box 缓存重新建立，必要时清除客户端及应用缓存。文本导入保留自定义 DNS，JSON 导入使用提交值，备份保存两个字段。自动健康检查使用配置端点；手动节点检测使用默认 DNS，不覆盖自定义映射 DNS 健康结果。

## 各类客户端设置

| 客户端 | 设置与路由 |
|---|---|
| Windows | 服务器使用 IPsec 时选 L2TP/IPsec + PSK，填写映射账号密码，认证方式需兼容服务器要求的 MS-CHAPv2。VPN 接口使用自动 DNS；全 IPv4 代理需启用 VPN 默认路由／远程默认网关并核对实际路由。 |
| macOS / iOS | 版本提供 L2TP/IPsec 时填写服务器、账号及共享密钥；可用时启用通过 VPN 发送所有流量并核对 DNS。无普通 L2TP 选项的客户端需要服务器启用 IPsec。 |
| Linux | 使用 L2TP 客户端，按需搭配 IPsec。pppd 启用 `usepeerdns`，通过 NetworkManager、systemd-resolved 或发行版 PPP hook 应用 DNS；收到 DNS 不代表系统解析器已采用。路由需另行配置。 |
| 路由器／网关 | 每条拨号会话使用独立账号。将所需 LAN IPv4 流量路由进 L2TP，源 NAT 为分配的 PPP 地址。路由器自身的 DNS 查询也需走此路径，LAN 策略路由未必涵盖本机查询。保留 VPN 服务器经原 WAN 的路由。 |
| Android／其他 | 确认该版本及客户端支持 L2TP/IPsec + PSK，不假定所有新系统都有兼容的内置客户端。 |

pppd 通过 IP-up hook 的 `DNS1`／`DNS2` 提供协商结果，也可写入 `/etc/ppp/resolv.conf`；这不是系统解析器配置文件。参见 [pppd 手册](https://github.com/ppp-project/ppp/blob/master/pppd/pppd.8)。使用网络管理服务时不要盲目覆盖 `/etc/resolv.conf`。

自动 DNS 未生效时，手动填写 1.1.1.1、8.8.8.8，并**将两者路由进 L2TP**。分别核对局域网 DNS、浏览器安全 DNS、应用 DoH 及分流规则；HTTPS 上的 DoH 不经 S2L 53 端口接管，而是跟随应用路由。

S2L 阻断进入 PPP 的 IPv6，但无法阻断客户端其他接口的 IPv6。需在客户端／路由器落实 IPv6 和断线时的阻断策略；要求失败即阻断时，不应在 VPN 断开后自动恢复 WAN 直连。

## 替代 VPN 协议

S2L 目前接入 L2TP，可选 L2TP/IPsec。以下是 2026 年 10 月的候选方案比较，不代表项目已实现。请核对固件与授权；协议识别不等于具有 VPN 客户端。

| 协议 | Panabit 标准／免费版 | 爱快免费版 | RouterOS 7 |
|---|---|---|---|
| L2TP | 现有部署已验证 | 有客户端 | 支持 |
| 原生 IPsec | 有模块；须核对 IKE 版本与选择器 | 须核对认证及路由 | 支持 |
| IKEv2/IPsec | 客户端支持未确认 | 官方 3.7 历史标注企业版 | 支持 |
| OpenVPN | 原生客户端未确认 | 有客户端文档 | 支持；v7 增加 UDP |
| WireGuard | 原生客户端未确认 | 官方 3.7 历史标注企业版 | 支持 |
| iWAN | 厂商私有协议 | 原生互通未确认 | 原生互通未确认 |

依据：[Panabit 标准版](https://panabit.com/Article?article_id=148)、[iWAN](https://www.panabit.com/Article?article_id=134)、[爱快授权历史](https://www.ikuai8.com/index.php?id=331&option=com_content&view=article)、[爱快 OpenVPN](https://www.ikuai8.com/support/ymgn/lyym/wlsz/vpn/openvpn.html)、[RouterOS 规格](https://manual.mikrotik.com/docs/getting-started/software-specifications/)、[RouterOS v7 变更](https://manual.mikrotik.com/docs/getting-started/upgrading-to-v7/)。尚未验证新版爱快免费版已开放上述企业版功能。

三类免费软路由共同使用时，保留 L2TP，另验证原生 IPsec 的互通；IPsec 需要对齐身份、算法与路由。仅爱快／RouterOS 可另评估 OpenVPN。WireGuard 在具备功能及授权时简单易用，但不是已验证的共同免费选项。每个同时连接的设备应有独立密钥与隧道 IP；多设备可共用同一 SOCKS5 出口，不必共用 VPN 身份。参阅 [WireGuard 对等端配置](https://www.wireguard.com/quickstart/)。

S2L 新增协议需要接入身份、路由、DNS、防火墙与恢复测试；现有 `ppp+` 规则不会自动保护 WireGuard／OpenVPN 接口。更换协议不能修复上游 SOCKS5、共用限速或路由策略问题。先在隔离环境试用，保留现有 iWAN/L2TP 路径。

## 接入验收

1. 核对 PPP IPv4、两个 DNS 及实际路由。Windows：`ipconfig /all`、`route print -4`；Linux：`ip -4 addr`、`ip -4 route get 1.1.1.1`，可用时执行 `resolvectl status`；macOS：`scutil --dns`、`netstat -rn -f inet`。VPN 服务器应仍经原网络可达。
2. 分别执行 `nslookup example.com 1.1.1.1`、`nslookup example.com 8.8.8.8`，再测试系统及应用的普通解析。自定义私有 DNS 还需查询仅该解析器能回答的名称，核对映射 DNS 计数；客户端直接取得公共 DNS 回复本身不能证明走了隧道。
3. 从真实客户端访问 HTTPS，核对所选 SOCKS5 出口。动态出口供应商可返回不同 IP，不能单凭差异判定节点切换。面板测速及协议检测不能代替此步。
4. 默认主动拒绝非 DNS UDP。应用可能回退 TCP；仅 UDP 业务需要允许策略和上游中继支持，并实测应用及 MTU/MSS 影响。
5. 维护窗口中断所选上游，确认业务及 DNS 失败且不回退到服务器公网出口；恢复后再次验证。另测 VPN 断线时客户端自身的 IPv6／直连阻断，测试网关多设备共享及同账号重复拨号拒绝。

能拨号但域名失败时，先查 DNS 是否采用、路由是否进隧道，再查 SOCKS5 到自定义解析器的连通及回复。默认节点 DNS 正常不代表私有解析器正常。健康检查、日志、恢复及防护边界见[操作指南](operations.md)。

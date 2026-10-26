# 用戶端接入與 DNS

[首頁](../../README.zh-Hant.md) · [English](../en/clients.md) · [繁體中文](../zh-Hant/clients.md) · [简体中文](../zh-Hans/clients.md)

## 撥號前準備

啟動映射，記錄伺服器位址、L2TP 帳密及系統設定中的接入模式。啟用 IPsec 時選 L2TP/IPsec 並填入相同 PSK；所有映射共用此設定。普通 L2TP 不加密，並非所有原生用戶端都支援，須核對作業系統及版本。一條映射只允許一條 PPP 會話；每個獨立撥號裝置使用不同映射及帳號。多台 LAN 裝置可共用路由器的一條會話。

伺服器需要用戶端可達的 IPv4。安裝及 HTTPS 輔助腳本要求公網 IPv4；自動取得公網位址不等於外部可接入。伺服器位於 NAT 後須設定入站轉發及防火牆。無入站轉發能力的 CGNAT 不適合直接遠端撥號。

| 路徑 | 要求 |
|---|---|
| 用戶端 → 普通 L2TP 伺服器 | UDP 1701 |
| 用戶端 → IPsec 伺服器 | UDP 500/4500；NAT-T |
| 伺服器 → 固定 SOCKS5 | TCP 可達、憑據有效且支援 CONNECT；僅允許的 UDP 業務需要 UDP ASSOCIATE 與可達的中繼 |
| SOCKS5 → 解析器及業務目標 | 可存取設定的 DoH HTTPS、TCP DNS 及應用目標 |

主機防火牆、雲端安全群組、上游 NAT 和用戶端網路都需允許所選模式。能開啟 HTTPS 面板不代表能撥號。IPsec 啟用後會阻擋未保護的 L2TP，不要開放裸 UDP 1701 作為替代。參閱 [strongSwan NAT-T 說明](https://docs.strongswan.org/docs/latest/features/natTraversal.html)。


S2L 現已在伺服器端隔離同一 NAT 公網位址下全部使用 L2TP 來源埠 1701 的用戶端：每個 IPsec 安全關聯使用獨立標記、連線追蹤區和內部對端埠，用戶端埠與固定 SOCKS5 映射不變。目前設定統一啟用 NAT-T（UDP 500/4500），無 NAT 用戶端也使用 UDP 封裝。Linux 真實撥號涵蓋兩個 1701 用戶端同時接入、金鑰輪換、重新撥號、DNS、UDP 和上游中斷。原生 Windows/macOS/路由器用戶端及伺服器本身位於 NAT 後的部署仍須分別實測，不能據此宣稱相容所有用戶端。

SOCKS5 UDP 僅放行代理的 **TCP 服務埠仍不足**。S2L 伺服器還需雙向到達 `UDP ASSOCIATE` 回傳的 UDP 中繼位址與連接埠，它可能是不同於 TCP 埠的動態埠；代理本身的出站 UDP 也需可用，並保持關聯的 TCP 控制連線。協商成功不等於 UDP 轉發成功。內建檢測驗證真實 UDP DNS 回包，不能證明所有 UDP 應用或 QUIC 正常。`reject-quic` 實際拒絕全部 UDP/443，並非識別 QUIC 協定；TCP MSS 不會改變 UDP 資料報大小。

若 TCP/DoH 正常、UDP ASSOCIATE 成功卻收不到資料報，應使用同一節點在另一個獲准的網路介面上對照測試。本機 VPN/TUN、策略路由或上游防火牆都可能丟棄中繼流量。TCP 控制連線和 UDP 必須使用同一出口介面，僅綁定 TCP 不夠。另一路徑成功能排除「節點完全不支援 UDP」，但不足以定位失敗路徑中的具體丟包設備。不得讓用戶端退回直連來繞過中繼故障。

## 下發 DNS 與實際解析器

PPP 在 IPCP 協商時下發 **1.1.1.1 和 8.8.8.8**。用戶端接受 VPN 自動 DNS。舊部署依[更新流程](operations.md)升級後，重新撥號取得 DNS；既有會話不會收到新的 PPP 選項。不要手動修改產生的 PPP 設定檔。

這兩個位址是隧道內 DNS 請求的入口識別。進入映射 TUN 的 TCP/UDP 53 請求均被接管，實際解析經映射的固定 SOCKS5。自訂解析器不改變下發位址；無需公開 DNS 監聽或開放 PPP 存取主機。如果 S2L 主機本身綁定了上述位址，應在用戶端改用其他非本機 DNS 目的位址並路由進隧道：本機 INPUT 會在 TUN 接管前被阻擋。

停止映射，在「編輯 → 進階選項」設定，儲存後啟動：

| 介面 / JSON 欄位 | 預設值 | 格式 |
|---|---|---|
| DNS 模式 / `dns_mode` | `auto` | `auto`、`doh`、`tcp` |
| DoH 位址 / `dns_doh_url` | `https://1.1.1.1/dns-query` | HTTPS URL，可含連接埠與路徑；不可含帳密、查詢參數、片段或空白 |
| TCP DNS 位址 / `dns_tcp_address` | `8.8.8.8:53` | `主機:連接埠` 或 `[IPv6]:連接埠`，1–65535；不支援帶介面範圍的 IPv6 |

留空恢復預設值；舊狀態檔缺少欄位時仍用相同預設值。解析器網域由 SOCKS5 遠端解析。SOCKS5 伺服器自身的網域仍可能使用 S2L 主機解析器，填入上游 IP 可避免這次啟動解析。IPv6 解析器需要 SOCKS5 支援，不代表開啟 PPP IPv6 轉發。

映射 JSON 範例片段：

```json
{
  "dns_mode": "tcp",
  "dns_doh_url": "",
  "dns_tcp_address": "10.20.0.53:53"
}
```

這是完整映射的一部分，不能單獨匯入。私有 DNS 必須能**從 SOCKS5 出口到達**。請選能回答所需公網及私有網域的解析器；尚無依網域分流的 DNS 規則。自動模式的兩個端點應提供一致的解析範圍；私有名稱不應回退至預設公共 DNS 時，配置兩個端點或使用強制模式。

自動模式在 DoH 查詢失敗時嘗試 TCP，主解析連續失敗後優先 TCP，恢復檢查可切回 DoH；不切換 SOCKS5 節點、不改成伺服器直連。強制模式只用指定傳輸。DNS 正常工作不要求 SOCKS5 支援 UDP，有效 NXDOMAIN 不觸發切換。修改端點後映射解析器及 sing-box 快取重新建立，必要時清除用戶端及應用快取。文字匯入保留自訂 DNS，JSON 匯入使用提交值，備份保存兩個欄位。自動健康檢查使用設定端點；手動節點檢測使用預設 DNS，不覆寫自訂映射 DNS 健康結果。

## 各類用戶端設定

| 用戶端 | 設定與路由 |
|---|---|
| Windows | 伺服器使用 IPsec 時選 L2TP/IPsec + PSK，填入映射帳密，認證方式需相容於伺服器要求的 MS-CHAPv2。VPN 介面使用自動 DNS；全 IPv4 代理需啟用 VPN 預設路由／遠端預設閘道並核對實際路由。 |
| macOS / iOS | 版本提供 L2TP/IPsec 時填入伺服器、帳號及共享密鑰；可用時啟用透過 VPN 傳送所有流量並核對 DNS。無普通 L2TP 選項的用戶端需要伺服器啟用 IPsec。 |
| Linux | 使用 L2TP 用戶端，按需搭配 IPsec。pppd 啟用 `usepeerdns`，透過 NetworkManager、systemd-resolved 或發行版 PPP hook 套用 DNS；收到 DNS 不代表系統解析器已採用。路由需另行設定。 |
| 路由器／閘道 | 每條撥號會話使用獨立帳號。將所需 LAN IPv4 流量路由進 L2TP，來源 NAT 為分配的 PPP 位址。路由器自身的 DNS 查詢也需走此路徑，LAN 策略路由未必涵蓋本機查詢。保留 VPN 伺服器經原 WAN 的路由。 |
| Android／其他 | 確認該版本及用戶端支援 L2TP/IPsec + PSK，不假定所有新系統都有相容的內建用戶端。 |

pppd 透過 IP-up hook 的 `DNS1`／`DNS2` 提供協商結果，也可寫入 `/etc/ppp/resolv.conf`；這不是系統解析器設定檔。參閱 [pppd 手冊](https://github.com/ppp-project/ppp/blob/master/pppd/pppd.8)。使用網路管理服務時不要盲目覆寫 `/etc/resolv.conf`。

自動 DNS 未生效時，手動填入 1.1.1.1、8.8.8.8，並**將兩者路由進 L2TP**。分別核對區域網路 DNS、瀏覽器安全 DNS、應用 DoH 及分流規則；HTTPS 上的 DoH 不經 S2L 53 埠接管，而是跟隨應用路由。

S2L 阻擋進入 PPP 的 IPv6，但無法阻擋用戶端其他介面的 IPv6。需在用戶端／路由器落實 IPv6 和斷線時的阻斷策略；要求失敗即阻斷時，不應在 VPN 斷開後自動恢復 WAN 直連。

## 替代 VPN 協定

S2L 目前接入 L2TP，可選 L2TP/IPsec。以下為 2026 年 10 月的候選方案比較，不代表專案已實作。請核對韌體與授權；協定識別不等於具有 VPN 用戶端。

| 協定 | Panabit 標準／免費版 | 愛快免費版 | RouterOS 7 |
|---|---|---|---|
| L2TP | 現有部署已驗證 | 有用戶端 | 支援 |
| 原生 IPsec | 有模組；須核對 IKE 版本與選擇器 | 須核對認證及路由 | 支援 |
| IKEv2/IPsec | 用戶端支援未確認 | 官方 3.7 歷史標示企業版 | 支援 |
| OpenVPN | 原生用戶端未確認 | 有用戶端文件 | 支援；v7 增加 UDP |
| WireGuard | 原生用戶端未確認 | 官方 3.7 歷史標示企業版 | 支援 |
| iWAN | 廠商私有協定 | 原生互通未確認 | 原生互通未確認 |

依據：[Panabit 標準版](https://panabit.com/Article?article_id=148)、[iWAN](https://www.panabit.com/Article?article_id=134)、[愛快授權歷史](https://www.ikuai8.com/index.php?id=331&option=com_content&view=article)、[愛快 OpenVPN](https://www.ikuai8.com/support/ymgn/lyym/wlsz/vpn/openvpn.html)、[RouterOS 規格](https://manual.mikrotik.com/docs/getting-started/software-specifications/)、[RouterOS v7 變更](https://manual.mikrotik.com/docs/getting-started/upgrading-to-v7/)。尚未驗證新版愛快免費版已開放上述企業版功能。

三類免費軟路由共同使用時，保留 L2TP，另驗證原生 IPsec 的互通；IPsec 需對齊身分、演算法與路由。僅愛快／RouterOS 可另評估 OpenVPN。WireGuard 在具備功能及授權時簡單易用，但不是已驗證的共同免費選項。每個同時連線的裝置應有獨立金鑰與隧道 IP；多裝置可共用同一 SOCKS5 出口，不必共用 VPN 身分。參閱 [WireGuard 對等端設定](https://www.wireguard.com/quickstart/)。

S2L 新增協定需要接入身分、路由、DNS、防火牆與恢復測試；現有 `ppp+` 規則不會自動保護 WireGuard／OpenVPN 介面。更換協定不能修復上游 SOCKS5、共用限速或路由策略問題。先在隔離環境試行，保留現有 iWAN/L2TP 路徑。

## 接入驗收

1. 核對 PPP IPv4、兩個 DNS 及實際路由。Windows：`ipconfig /all`、`route print -4`；Linux：`ip -4 addr`、`ip -4 route get 1.1.1.1`，可用時執行 `resolvectl status`；macOS：`scutil --dns`、`netstat -rn -f inet`。VPN 伺服器應仍經原網路可達。
2. 分別執行 `nslookup example.com 1.1.1.1`、`nslookup example.com 8.8.8.8`，再測系統及應用的普通解析。自訂私有 DNS 還需查詢僅該解析器能回答的名稱，核對映射 DNS 計數；用戶端直接取得公共 DNS 回覆本身不能證明走了隧道。
3. 從真實用戶端存取 HTTPS，核對所選 SOCKS5 出口。動態出口供應商可回傳不同 IP，不能單憑差異判定節點切換。面板測速及協定檢測不能代替此步。
4. 預設主動拒絕非 DNS UDP。應用可能回退 TCP；僅 UDP 業務需要允許策略和上游中繼支援，並實測應用及 MTU/MSS 影響。
5. 維護時段中斷所選上游，確認業務及 DNS 失敗且不回退至伺服器公網出口；恢復後再次驗證。另測 VPN 斷線時用戶端自身的 IPv6／直連阻斷，測試閘道多裝置共用及同帳號重複撥號拒絕。

能撥號但網域失敗時，先查 DNS 是否採用、路由是否進隧道，再查 SOCKS5 到自訂解析器的連通及回覆。預設節點 DNS 正常不代表私有解析器正常。健康檢查、日誌、恢復及防護邊界見[操作指南](operations.md)。

# S2L

[English](README.md) · [繁體中文](README.zh-Hant.md) · [简体中文](README.zh-Hans.md)

S2L 在 Linux 上管理 L2TP 用戶端連線，透過 sing-box、策略路由與 iptables 將流量導向指定 SOCKS5 上游。Go 服務內嵌 Vue 3 / Element Plus 管理介面，支援節點與映射管理、批次匯入、啟停、重啟恢復、上下行測速與系統設定。管理介面支援簡體中文、繁體中文與英文，使用表單登入與工作階段驗證；安裝訊息為簡體中文。

僅在取得授權的主機、網路與上游服務上使用。部署前閱讀[法律與合規說明](docs/zh-Hant/legal.md)，尤其涉及中國大陸或跨境連線時。軟體不提供監管許可，也不保證匿名性。

## 安裝與安全

安裝程式支援 Ubuntu/Debian 的 amd64、arm64，建議基準為 Ubuntu Server 24.04 LTS amd64。隧道需要 Linux、systemd、root 權限與 TUN；非 Linux 建置無法提供隧道服務。

**預設普通 L2TP 不加密。** 管理 HTTPS 不會加密隧道流量。選配 IPsec 使用 IKEv1 與共用 PSK，須驗證用戶端相容性；SOCKS5 本身亦不加密。UDP 能力取決於上游。PPP IPv4 轉發限制於對應 TUN，PPP IPv6 被阻擋；仍須驗證用戶端路由與防火牆相容性，不提供匿名性保證。

使用專用主機並保留安全更新。安裝會修改套件、轉送、PPP/L2TP、防火牆及系統服務。新安裝優先使用下方一鍵腳本；執行前審閱腳本，其預設取得 `main` 分支。

```bash
curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_install.sh | sudo bash
```

僅允許可信管理 IP 存取安裝輸出的 HTTPS TCP 埠。安裝產生 365 天自簽憑證與 16 字元隨機管理密碼，後端監聽 `127.0.0.1:8080`。信任憑證前透過可信管道核對 SHA-256 指紋。省略 `HTTPS_PORT` 時選用 10000–60000 空閒埠，不得使用 8080；必要時以 `PUBLIC_IP` 指定公網 IPv4。

如需固定 HTTPS 埠，例如 8443，可使用下列命令，並放行對應 TCP 埠：

```bash
curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_install.sh | sudo env HTTPS_PORT=8443 bash
```

登入顯示的 HTTPS 網址，新增授權節點，建立映射並啟動至 `running`。用戶端使用伺服器 IP 與映射帳密。普通 L2TP 使用 UDP 1701；IPsec 需要 UDP 500/4500 及 ESP（IP 協定 50）。先閱讀操作指南再開放連接埠。更新既有安裝不要重跑安裝程式。從已審閱原始碼工作副本安裝的方式見[開發指南](docs/zh-Hant/development.md)。

持續 PPP 防洩漏、上游 TCP／UDP／DNS 獨立健康監控與 sing-box/TUN 自動恢復詳見[操作指南](docs/zh-Hant/operations.md)。既有安裝須先安裝新的防護服務，再重新啟動 xl2tpd。

## 升級

既有預設安裝優先使用一鍵升級。腳本先編譯、備份，再更新程式與必要防護服務並保留設定。執行中的用戶端須重新撥號，原本停止的服務保持停止；失敗處理與手動方式見[操作指南](docs/zh-Hant/operations.md)。

```bash
curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_update.sh | sudo bash
```

## 文件

- [操作、備份、更新與疑難排解](docs/zh-Hant/operations.md)
- [架構、設定與開發](docs/zh-Hant/development.md)
- [法律與合規](docs/zh-Hant/legal.md)
- [安全問題回報（英文）](SECURITY.md)

需要 Go 1.22 以上及相容 Node.js/npm。依序執行 `npm --prefix web ci`、`npm --prefix web run build`、`go build -o bin/s2l ./cmd/s2l`。前端變更須提交 `web/dist/`。

S2L 原創程式碼與文件採用 [Apache-2.0](LICENSE)。參閱 [NOTICE](NOTICE) 與[第三方聲明](THIRD_PARTY_NOTICES.md)，依賴元件保留各自條款。軟體授權不代表取得監管許可。

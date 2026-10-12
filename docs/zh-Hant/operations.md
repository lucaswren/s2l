# 操作指南

[首頁](../../README.zh-Hant.md) · [English](../en/operations.md) · [繁體中文](../zh-Hant/operations.md) · [简体中文](../zh-Hans/operations.md)

先閱讀[法律說明](legal.md)與 README 的安全限制。保留安全更新，等待套件管理鎖釋放；會中斷連線的操作安排在維護時段。

## 新安裝

新安裝優先使用一鍵腳本。請使用專用 Ubuntu/Debian 主機，執行前審閱腳本：

```bash
curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_install.sh | sudo bash
```

腳本以 root 執行，預設取得 `main` 分支；未指定 `HTTPS_PORT` 時選用隨機空閒 TCP 埠。僅允許可信管理 IP 存取輸出的管理埠。固定埠設定見 README；從已審閱原始碼工作副本安裝的方式見開發指南。既有安裝請使用下方更新流程。

## 上游健康與防洩漏

映射 `running` 與上游健康分別顯示。監控透過 SOCKS5 認證與轉發取得小型 HTTPS 回應，使用 Cloudflare 與 Google 作為替代目標；這些供應商會收到經上游送出的探測連線。一次失敗標記為不穩定，連續三次失敗標記為不可用；連續兩次成功才恢復正常。正常約每 10 秒檢查，失敗後按 5、10、20、40、最多 60 秒退避，另加探測與排程時間；同時最多處理四條映射。這僅檢查上游 TCP，不保證 UDP、DNS 或完整用戶端路徑。

上游中斷時保留 L2TP，已失敗的應用連線須重新建立。sing-box 程序或 TUN 消失時，以受限頻率自動恢復，不停止 L2TP；失敗顯示於健康狀態與服務日誌。目前不自動切換其他節點；更換映射節點前先停止映射。執行中映射使用的節點不可修改連線設定。

S2L 必須使用專用主機：持續防護涵蓋**所有 `ppp+` 介面**。PPP IPv4 僅允許已設定來源子網經對應 TUN 轉發；其他轉發及所有 PPP 對主機本機的存取均丟棄，PPP IPv6 亦阻擋。TUN 消失時，終止查找的不可達路由阻止回退至主路由表。停止 S2L 或映射後仍保留阻斷規則，避免既有 PPP 連線由主機出口繞行。`s2l-guard.service` 在 xl2tpd 前安裝規則，失敗時阻止 xl2tpd/S2L 啟動。勿清空這些規則或讓其他防火牆管理器覆寫。用戶端分流與其他網路介面不在伺服器控制範圍內。

既有安裝須建置已審閱版本，停止 **S2L 與 xl2tpd**、備份舊部署並更換程式。從已審閱工作副本安裝新服務單元後再啟動（以下使用預設 `/opt/s2l` 路徑）：

```bash
sudo install -m 644 deploy/systemd/s2l-guard.service /etc/systemd/system/s2l-guard.service
sudo install -m 644 deploy/systemd/s2l.service /etc/systemd/system/s2l.service
sudo mkdir -p /etc/systemd/system/xl2tpd.service.d
sudo install -m 644 deploy/systemd/xl2tpd-s2l.conf /etc/systemd/system/xl2tpd.service.d/s2l.conf
sudo systemctl daemon-reload
sudo systemctl enable s2l-guard.service
sudo systemctl restart s2l-guard.service
sudo systemctl start xl2tpd s2l
```


## 存取與連線

使用安裝輸出的 HTTPS 網址與帳密；在主機防火牆及雲端安全群組限制管理埠來源 IP，後端 8080 不對外開放。新增授權 SOCKS5 節點與映射並啟動；`running` 不代表端到端驗證成功，須從實際用戶端檢查出口、DNS、IPv6 處理與失敗行為。

普通 L2TP 預設使用 UDP 1701，**不加密**。管理 HTTPS 僅保護網頁/API。IPsec 使用 IKEv1 與共用 PSK，需要 UDP 500/4500 及 ESP（IP 協定 50），須驗證 NAT 與用戶端相容性。依安全政策選用相容的加密傳輸。

系統設定可啟用 IPsec，PSK 至少 12 字元，所有映射共用；留空保留原密鑰。啟用後阻止普通 L2TP，關閉後用戶端須清空 PSK；切換模式或密鑰後重新撥號。設定或防火牆操作失敗時會嘗試還原；還原失敗時須透過 SSH 檢查。偵測既有 IPsec 服務衝突時拒絕修改，備份中繼資料為 `/opt/s2l/ipsec-backup.json`。

測速經所選上游連至 Cloudflare，預設每方向 5 MB，會消耗流量；結果不代表完整 L2TP 用戶端效能，單向失敗仍可能保留另一方向結果。

## 管理

執行 `s2l` 開啟選單，或 `s2l 編號` 執行一次操作；普通使用者會要求 sudo。

| 編號 | 操作 |
|---|---|
| 1 | 網址與帳號，不顯示密碼 |
| 2 / 3 | 狀態 / 重啟 |
| 4 | 最近 80 筆日誌 |
| 5 / 6 | 啟動 / 停止 |
| 7 / 8 | 管理密碼 / 帳號 |
| 9 / 10 | 管理埠 / SSH 埠 |
| 0 | 離開 |

管理密碼 12–128 字元，安裝與 CLI 限制可用字元。CLI 帳密變更重啟服務，可能中斷隧道；網頁變更立即生效，無須重啟，之後重新登入。網址優先採用 `public_url`，否則依監聽與公網 IP 產生，偵測退回本機地址時提示 NAT。

修改連接埠前放行新埠，保留現有 SSH 連線，使用新網址或 `ssh -p 新埠 使用者@伺服器IP` 驗證後才關閉原連線。HTTPS 埠變更重載 Nginx 並保留後端與憑證，驗證或重載失敗時會嘗試還原。SSH 變更檢查 `sshd` 設定與監聽，支援 Ubuntu `ssh.socket`，備份中繼資料為 `/opt/s2l/ssh-port-backup.json`。

網頁 root 密碼功能限已登入管理員透過 HTTPS 使用；接受 12–128 可見 ASCII 字元或產生 16 字元密碼。只修改 Linux root 密碼，不啟用 SSH 密碼驗證、不解鎖帳號，也不修改網頁密碼；須另外驗證登入。

## HTTPS 與備份

路由表編號限 1–252，253–255 為 Linux 保留表；僅使用專屬於 S2L 的表，清理會清空所選表。舊映射若使用保留編號，啟動前須修正，勿手動清空系統表。執行階段檔案改存私有 `/run/s2l`（0700），日誌為 0600；更新服務單元或由 root 程序建立目錄。舊 `/tmp/s2l-*` 不自動搬移，停止舊程序後僅清除已確認屬於本專案的殘留檔。

既有安裝在放行指定埠並審閱腳本後，可於專案目錄執行：

```bash
sudo bash script/setup_https.sh YOUR_PUBLIC_IPV4 8443
s2l 1
```

信任 `/opt/s2l/tls/self-signed.crt` 前核對輸出的 SHA-256 指紋，勿分享私鑰。自簽憑證不受公開信任，不需 TCP 80 或公開 CA 續期。有效期 365 天；到期前重新執行管理腳本，有效期不足一天時重新產生。既有 HTTPS 安裝省略埠參數會保留原埠，設定失敗時嘗試還原；還原失敗時，須透過 SSH 或供應商主控台檢查備份並處理。

設定為 `/opt/s2l/config.json`，狀態為 `/opt/s2l/data/state.json`；TLS 私鑰、PPP 帳密、Nginx/systemd 與 SSH/IPsec 修改檔也須保護。`/run/s2l/s2l-*` 產生檔與日誌可能含敏感資訊。限制權限並加密異地備份。

更新前記錄版本，在維護時段停止 S2L，備份一致的設定、狀態、舊執行檔與系統網路設定。在獨立工作副本中建置已審閱版本，驗證後停服，只替換應用程式執行檔再啟動；管理腳本與服務單元的變更應另行審閱後安裝。不要重跑新安裝腳本，遠端腳本可能重設原始碼工作副本。啟動後實測映射與流量；失敗時停服並恢復已驗證快照、舊程式及相符的狀態設定。僅回退執行檔不一定能還原資料格式或系統變更，還原須保持正確權限與擁有者。

## 疑難排解

```bash
sudo systemctl status s2l xl2tpd nginx --no-pager
sudo journalctl -u s2l -n 80 --no-pager
sudo nginx -t
sudo ss -lntup
ip rule show
ip route show table all
```

網頁失敗查網址、憑證、Nginx 與防火牆；映射失敗查錯誤、TUN、上游與 `/run/s2l/s2l-TUN_NAME.log`。撥號失敗查帳密、模式、NAT、xl2tpd 及啟用時的 `s2l-ipsec` 日誌。撥號成功但無流量查子網衝突、路由、DNS 與上游。SSH 變更失敗保留連線並使用供應商主控台等授權救援管道。

分享前清除日誌中的秘密；勿清空整台主機的防火牆排障。目前無完整自動移除流程，移除須依備份恢復並確認共用服務，不可盲目刪除 PPP、Nginx 或防火牆設定。

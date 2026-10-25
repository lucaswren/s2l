# 架構、設定與開發

[首頁](../../README.zh-Hant.md) · [English](../en/development.md) · [繁體中文](../zh-Hant/development.md) · [简体中文](../zh-Hans/development.md)

[用戶端接入、DNS 與驗收](clients.md)

L2TP/PPP 用戶端連至 xl2tpd；S2L 將映射帳密與 IPv4 子網連結至 sing-box TUN、策略路由表及防火牆規則，再由 sing-box 連至 SOCKS5 上游。管理介面的 Nginx HTTPS 代理與此資料路徑分開。

`cmd/s2l/` 負責啟動與關閉；`internal/api/` 提供網頁與指令碼工作階段驗證及管理 API；`internal/service/` 編排映射生命週期。`internal/singbox/`、`network/`、`l2tp/` 負責程序、路由、防火牆與 PPP；`store/`、`model/`、`config/` 處理狀態、模型與設定。`web/src/` 是前端原始碼，`web/dist/` 是需提交的內嵌資源；`script/` 與 `deploy/systemd/` 管理安裝及服務。

`internal/dnsproxy/` 負責每條映射的 DoH/TCP；`internal/diagnostics/` 與 `speedtest/` 負責分階段檢測；`history/`、`operations/`、`debuglog/` 分別保存有界歷史、事件通知與暫存日誌。`web/src/i18n/` 集中管理三語言文案，頁面共用 `components/` 與 `utils/`。

## 設定

未指定 `-config` 時使用預設設定；明確指定的檔案必須存在且內容為 JSON 物件。檔案不存在或內容為 `null` 時會停止啟動，不會悄悄改用預設值。

以 `config.example.json` 為起點，在合適測試主機使用 `./bin/s2l -config /path/to/config.json`。`listen` 預設 `127.0.0.1:8080`；`admin_user` 與 `admin_pass` 須一起設定，密碼至少 12 字元。範例帳密空白，只允許本機監聽，但反向代理仍可能將未驗證的服務公開，代理前必須設定帳密。

`singbox_bin` 程序預設為 `sing-box`，範例為 `/usr/local/bin/sing-box`；`data_dir` 預設 `./data`，相對於工作目錄，含 `state.json`。安裝服務工作目錄為 `/opt/s2l`。`chap_secrets`、`xl2tpd_conf`、`ppp_options` 分別預設 `/etc/ppp/chap-secrets`、`/etc/xl2tpd/xl2tpd.conf`、`/etc/ppp/options.xl2tpd`。`auto_restore` 預設 `true`，恢復已保存為 `running` 的映射。腳本管理的 `public_url` 等中繼資料不屬於 Go `Config` 欄位。

## 從已審閱原始碼安裝

文件優先介紹一鍵安裝。若需安裝指定的已審閱版本，請將 `YOUR_REVIEWED_COMMIT` 替換為該提交 ID，並在新主機執行下列命令；執行最後一步前，審閱安裝腳本與相關管理腳本。

```bash
git clone https://github.com/lucaswren/s2l.git
cd s2l
git checkout YOUR_REVIEWED_COMMIT
sudo env HTTPS_PORT=8443 bash script/install.sh
```

## 建置與貢獻

```bash
npm --prefix web ci
npm --prefix web run build
go build -o bin/s2l ./cmd/s2l
go test ./...
python3 -m unittest discover -s script/tests
```

`make build` 包含前端建置；`make test` 執行 Go 測試。`npm --prefix web run dev` 的 API 代理至本機 8080。非 Linux 測試不驗證實際 Linux 隧道，整合驗證須使用授權、可丟棄的 Linux VM。`make run`、`make install-service` 可能修改主機與 PPP，且不等同完整 HTTPS 安裝。

變更須說明觸發條件、結果與相關驗證；勿提交執行設定、狀態、依賴或秘密。前端變更須重建並提交 `web/dist/`。文件先更新英文，再繁體、簡體中文，保持命令、預設值與法律語意一致。法律來源使用官方連結並記錄查核日期；僅提交有權按 Apache-2.0 授權的內容，保留第三方聲明。依賴更新後執行 `python3 script/collect_frontend_licenses.py` 並提交聲明清單。

文件、介面與服務描述的展示名稱統一為 **S2L**；執行檔、模組與儲存庫路徑、命令、設定鍵及既有程式識別標記保留小寫 `s2l`。協定名稱統一為 SOCKS5、L2TP、IPsec；須區分伺服器端測速與用戶端完整路徑，以及嘗試還原與已驗證的恢復結果。

中文文件依文字系統使用 `zh-Hant`（繁體中文）與 `zh-Hans`（简体中文），遵循 [BCP 47 文字子標籤](https://www.rfc-editor.org/rfc/rfc5646.html#section-2.2.3)。語言標籤表示文字系統，法律適用地區另於內容中說明。檔名、導覽連結與 HTML 語言屬性須保持一致；提交前執行 `python3 script/check_docs.py` 檢查本地文件連結。


## 診斷整合測試

```bash
# 先建置 web/dist；需要 Chrome、Node.js 22+ 和 Python 3。
node script/test_ui.mjs
node script/test_auth_ui.mjs

# 僅限隔離 Linux 測試主機，需核心 PPP/L2TP 支援及測試相依套件。
go test -c -o /tmp/s2l-l2tp.test ./internal/service
sudo python3 script/test_l2tp_integration.py --test-binary /tmp/s2l-l2tp.test --singbox /absolute/path/to/sing-box
for transport in ipsec ipsec-nat ipsec-shared-nat ipsec-shared-nat-1701; do
  sudo python3 script/test_l2tp_integration.py --test-binary /tmp/s2l-l2tp.test --singbox /absolute/path/to/sing-box --transport "$transport"
done
```

L2TP 測試隔離掛載、網路與 PID 命名空間，透過真實 PPP IPCP 驗證 DNS 下發，檢查自訂 DoH、同節點 TCP DNS 回退及用戶端 UDP/TCP DNS 查詢，並驗證雙固定出口。單一及雙上游中斷時，HTTPS 與未快取 DNS 請求須失敗，PPP 維持連線。另設可直接到達且憑證驗證成功的 HTTPS 出口作正向對照；故障與 TUN 消失期間，其出站封包計數須維持零。也檢查 UDP 主動拒絕、程序恢復及規則修復。瀏覽器測試涵蓋桌面、390/320 像素、語言切換與未儲存表單；驗證測試使用真實後端及暫存資料，涵蓋登入、工作階段保持與登出。頁面測試不能取代 Linux 資料路徑測試。

額外模式需要 strongSwan/starter、conntrack 及核心 XFRM、連線追蹤區與 NAT INPUT 支援。`ipsec` 驗證無 NAT 路由器的用戶端（仍強制 NAT-T）；`ipsec-nat` 使用不同轉換後位址；`ipsec-shared-nat` 共用公網位址、來源埠分別為 1701/1702；`ipsec-shared-nat-1701` 同時共用公網位址及來源埠 1701。均使用正式設定與 updown 輔助程式，驗證 DNS、各節點固定出口、UDP allow/reject-quic、上游/TUN 故障及直連出站計數為零。相同埠模式還延遲 updown 重現早到封包、線上修復防火牆並刪除回包標記、輪換 CHILD_SA，並讓一個用戶端斷線重撥，核對另一用戶端不受影響、退役連線追蹤區為空。入站和出站防護缺失必須被偵測並修復。外部節點探測單獨執行；Linux 對端通過不代表原生 Windows/macOS 已驗證。

相同埠測試另將分配計數設為 65,536，執行四輪端點回收，共新增七次真實重撥，其中三輪為雙用戶端並發。保留真實的 30 秒隔離期，由正式 updown 自動完成回收。殘留 UDP socket 或控制查詢失敗必須阻止重用，舊端點回包必須被出站防護攔截；單用戶端回收期間持續探測另一用戶端的固定出口。每輪複核 DNS、UDP 策略、SOCKS 身分及 ESP，隨後繼續執行上游/TUN 中斷與直連計數為零的故障測試。Python 回歸涵蓋 16 個並發程序分配新舊端點、45,540 次模擬生命週期、不完整/變化的全域快照、半開隧道、可恢復的中斷記錄及查詢失敗。長迴圈中的核心觀察使用模擬，不代表進行了 45,540 次真實 VPN 撥號。

# 架構、設定與開發

[首頁](../../README.zh-TW.md) · [English](../en/development.md) · [简体中文](../zh-CN/development.md)

L2TP/PPP 用戶端連至 xl2tpd；s2l 將映射帳密與 IPv4 子網連結至 sing-box TUN、策略路由表及防火牆規則，再由 sing-box 連至 SOCKS5 上游。管理介面的 Nginx HTTPS 代理與此資料路徑分開。

`cmd/s2l/` 負責啟動與關閉；`internal/api/` 提供 Basic Auth 管理 API；`internal/service/` 編排映射生命週期。`internal/singbox/`、`network/`、`l2tp/` 負責程序、路由、防火牆與 PPP；`store/`、`model/`、`config/` 處理狀態、模型與設定。`web/src/` 是前端原始碼，`web/dist/` 是需提交的內嵌資源；`script/` 與 `deploy/systemd/` 管理安裝及服務。

## 設定

以 `config.example.json` 為起點，在合適測試主機使用 `./bin/s2l -config /path/to/config.json`。`listen` 預設 `127.0.0.1:8080`；`admin_user` 與 `admin_pass` 須一起設定，密碼至少 12 字元。範例帳密空白，只允許本機監聽，但反向代理仍可能將未驗證的服務公開，代理前必須設定帳密。

`singbox_bin` 程序預設為 `sing-box`，範例為 `/usr/local/bin/sing-box`；`data_dir` 預設 `./data`，相對於工作目錄，含 `state.json`。安裝服務工作目錄為 `/opt/s2l`。`chap_secrets`、`xl2tpd_conf`、`ppp_options` 分別預設 `/etc/ppp/chap-secrets`、`/etc/xl2tpd/xl2tpd.conf`、`/etc/ppp/options.xl2tpd`。`auto_restore` 預設 `true`，恢復已保存為 `running` 的映射。腳本管理的 `public_url` 等中繼資料不屬於 Go `Config` 欄位。

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

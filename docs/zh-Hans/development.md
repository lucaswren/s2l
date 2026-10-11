# 架构、配置与开发

[首页](../../README.zh-Hans.md) · [English](../en/development.md) · [繁體中文](../zh-Hant/development.md) · [简体中文](../zh-Hans/development.md)

L2TP/PPP 客户端连接 xl2tpd；S2L 将映射账号密码与 IPv4 子网关联至 sing-box TUN、策略路由表及防火墙规则，再由 sing-box 连接 SOCKS5 上游。管理界面的 Nginx HTTPS 代理与此数据路径分开。

`cmd/s2l/` 负责启动与关闭；`internal/api/` 提供 Basic Auth 管理 API；`internal/service/` 编排映射生命周期。`internal/singbox/`、`network/`、`l2tp/` 负责进程、路由、防火墙与 PPP；`store/`、`model/`、`config/` 处理状态、模型与配置。`web/src/` 是前端源码，`web/dist/` 是须提交的内嵌资源；`script/` 与 `deploy/systemd/` 管理安装及服务。

## 配置

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

`make build` 包含前端构建；`make test` 执行 Go 测试。`npm --prefix web run dev` 的 API 代理至本机 8080。非 Linux 测试不验证实际 Linux 隧道，集成验证须使用授权、可丢弃的 Linux VM。`make run`、`make install-service` 可能修改主机与 PPP，且不等同完整 HTTPS 安装。

变更须说明触发条件、结果与相关验证；勿提交运行配置、状态、依赖或秘密。前端变更须重建并提交 `web/dist/`。文档先更新英文，再繁体、简体中文，保持命令、默认值与法律语义一致。法律来源使用官方链接并记录核查日期；仅提交有权按 Apache-2.0 许可的内容，保留第三方声明。依赖更新后执行 `python3 script/collect_frontend_licenses.py` 并提交声明清单。

文档、界面与服务描述的展示名称统一为 **S2L**；可执行文件、模块与仓库路径、命令、配置键及现有程序识别标记保留小写 `s2l`。协议名称统一为 SOCKS5、L2TP、IPsec；须区分服务器端测速与客户端完整路径，以及尝试回滚与已验证的恢复结果。

中文文档按文字体系使用 `zh-Hant`（繁體中文）与 `zh-Hans`（简体中文），遵循 [BCP 47 文字子标签](https://www.rfc-editor.org/rfc/rfc5646.html#section-2.2.3)。语言标签表示文字体系，法律适用地区另在内容中说明。文件名、导航链接与 HTML 语言属性须保持一致；提交前执行 `python3 script/check_docs.py` 检查本地文档链接。

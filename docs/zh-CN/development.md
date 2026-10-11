# 架构、配置与开发

[首页](../../README.zh-CN.md) · [English](../en/development.md) · [繁體中文](../zh-TW/development.md)

L2TP/PPP 客户端连接 xl2tpd；s2l 将映射账号密码与 IPv4 子网关联至 sing-box TUN、策略路由表及防火墙规则，再由 sing-box 连接 SOCKS5 上游。管理界面的 Nginx HTTPS 代理与此数据路径分开。

`cmd/s2l/` 负责启动与关闭；`internal/api/` 提供 Basic Auth 管理 API；`internal/service/` 编排映射生命周期。`internal/singbox/`、`network/`、`l2tp/` 负责进程、路由、防火墙与 PPP；`store/`、`model/`、`config/` 处理状态、模型与配置。`web/src/` 是前端源码，`web/dist/` 是须提交的内嵌资源；`script/` 与 `deploy/systemd/` 管理安装及服务。

## 配置

以 `config.example.json` 为起点，在合适测试主机使用 `./bin/s2l -config /path/to/config.json`。`listen` 默认 `127.0.0.1:8080`；`admin_user` 与 `admin_pass` 须一起配置，密码至少 12 字符。示例账号密码为空，只允许本机监听，但反向代理仍可能将未验证的服务公开，代理前必须设置账号密码。

`singbox_bin` 程序默认为 `sing-box`，示例为 `/usr/local/bin/sing-box`；`data_dir` 默认 `./data`，相对于工作目录，含 `state.json`。安装服务工作目录为 `/opt/s2l`。`chap_secrets`、`xl2tpd_conf`、`ppp_options` 分别默认 `/etc/ppp/chap-secrets`、`/etc/xl2tpd/xl2tpd.conf`、`/etc/ppp/options.xl2tpd`。`auto_restore` 默认 `true`，恢复已保存为 `running` 的映射。脚本管理的 `public_url` 等元数据不属于 Go `Config` 字段。

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

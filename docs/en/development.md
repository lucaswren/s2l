# Architecture and development

[Home](../../README.md) · [繁體中文](../zh-TW/development.md) · [简体中文](../zh-CN/development.md)

## Data path and responsibilities

An L2TP/PPP client connects to xl2tpd. S2L associates mapping credentials and an IPv4 subnet with a sing-box TUN interface, policy routing table and firewall rules. sing-box forwards mapped traffic to the selected SOCKS5 node. Nginx terminates management HTTPS and proxies to the Go HTTP backend; it is separate from the tunnel path.

| Path | Responsibility |
|---|---|
| `cmd/s2l/` | Startup, configuration validation, restore, shutdown |
| `internal/api/` | Basic-auth protected management API and SPA delivery |
| `internal/service/` | Mapping lifecycle and rollback orchestration |
| `internal/singbox/`, `internal/network/`, `internal/l2tp/` | Process configuration, Linux routing/firewall, PPP/xl2tpd integration |
| `internal/store/`, `internal/model/`, `internal/config/` | Persistent state, models and runtime configuration |
| `internal/speedtest/` | SOCKS5 throughput and latency probes |
| `web/src/`, `web/dist/` | Vue sources and committed embedded assets |
| `script/`, `deploy/systemd/` | Privileged installation, management helpers and units |

## Configuration

Start with `config.example.json`; run `./bin/s2l -config /path/to/config.json` only on a suitable test host. Relative `data_dir` is resolved from the working directory; the installed service works in `/opt/s2l`.

| Key | Default / purpose |
|---|---|
| `listen` | `127.0.0.1:8080`, backend HTTP listener |
| `admin_user`, `admin_pass` | Empty in the example; configure both together, password at least 12 characters |
| `singbox_bin` | Program default `sing-box`; example uses `/usr/local/bin/sing-box` |
| `data_dir` | `./data`; contains `state.json` |
| `chap_secrets` | `/etc/ppp/chap-secrets` |
| `xl2tpd_conf` | `/etc/xl2tpd/xl2tpd.conf` |
| `ppp_options` | `/etc/ppp/options.xl2tpd` |
| `auto_restore` | `true`; restore mappings persisted as `running` |

The example intentionally has no credentials. Empty credentials are accepted only with a loopback listener, but a reverse proxy can expose that unauthenticated backend: set credentials before proxying. Script-managed metadata such as `public_url` is distinct from the Go `Config` fields. Do not copy development configuration into a public deployment.

## Build, tests and contributions

From the repository root:

```bash
npm --prefix web ci
npm --prefix web run build
go build -o bin/s2l ./cmd/s2l
go test ./...
python3 -m unittest discover -s script/tests
```

`make build` also builds the frontend; `make test` runs Go tests. `npm --prefix web run dev` starts Vite with an API proxy to localhost:8080. Non-Linux builds exercise stubs, not real Linux integration. Automated tests do not prove firewall, PPP, IPsec or client compatibility; use a disposable authorized Linux VM for privileged integration checks. Do not run `make run` or `make install-service` casually: these can modify host services and PPP files, and are not equivalent to the full HTTPS installer.

Keep changes focused, explain their trigger and behavior, and include relevant validation. Never commit runtime configuration, state, dependencies or secrets. Commit rebuilt `web/dist/` with frontend edits. Documentation should be updated in English first, then Traditional Chinese, then Simplified Chinese; retain matching commands, defaults and legal meaning. Link official legal sources with a check date. Contribute only content you have the right to submit under Apache-2.0; retain third-party notices. Run `python3 script/collect_frontend_licenses.py` after dependency updates and commit the generated inventory.

Use **S2L** as the display name in documentation, UI labels and service descriptions. Keep executable names, module/repository paths, commands, configuration keys and existing machine-recognized markers lowercase (`s2l`). Use SOCKS5, L2TP and IPsec consistently; distinguish server-side probes from client-path measurements and rollback attempts from verified recovery.

# Architecture and development

[Home](../../README.md) · [English](../en/development.md) · [繁體中文](../zh-Hant/development.md) · [简体中文](../zh-Hans/development.md)

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

Omitting `-config` uses defaults. An explicitly specified file must exist and contain a JSON object; a missing file or `null` stops startup instead of silently using defaults.

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

## Installation from a reviewed checkout

The one-command installer is the default documented method. To install a specific reviewed revision instead, replace `YOUR_REVIEWED_COMMIT` with its commit ID and run the following on a new host. Review the installation script and its helpers before the final command.

```bash
git clone https://github.com/lucaswren/s2l.git
cd s2l
git checkout YOUR_REVIEWED_COMMIT
sudo env HTTPS_PORT=8443 bash script/install.sh
```

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

Chinese editions use the script-based tags `zh-Hant` (繁體中文) and `zh-Hans` (简体中文), following [BCP 47 script subtags](https://www.rfc-editor.org/rfc/rfc5646.html#section-2.2.3). These tags identify the writing system; legal jurisdiction is stated separately in the content. Keep filenames, navigation links and HTML language attributes consistent. Run `python3 script/check_docs.py` to check local documentation links before submitting changes.


## Diagnostic integration tests

```bash
# Build web/dist first. Requires local Chrome, Node.js 22+ and Python 3.
node script/test_ui.mjs

# Disposable Linux host only: requires PPP/L2TP kernel support and test dependencies.
go test -c -o /tmp/s2l-l2tp.test ./internal/service
sudo python3 script/test_l2tp_integration.py --test-binary /tmp/s2l-l2tp.test --singbox /absolute/path/to/sing-box
```

The L2TP runner isolates mount, network and PID namespaces. It checks two fixed exits, DNS over TCP, active UDP rejection, process recovery and missing-rule repair against local fixtures. Browser checks cover desktop and 390/320-pixel layouts using mock APIs; they do not replace Linux data-plane tests.

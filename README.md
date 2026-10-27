# S2L

[English](README.md) · [繁體中文](README.zh-Hant.md) · [简体中文](README.zh-Hans.md)

[Client setup, DNS and acceptance checks](docs/en/clients.md)

S2L manages Linux L2TP client connections and routes their traffic through selected SOCKS5 upstreams using sing-box, policy routing and iptables. The Go backend provides the management API and an embedded Vue 3 / Element Plus web interface.

Use only on authorized systems, networks and upstream services. Read the [legal and compliance notes](docs/en/legal.md) before deployment, especially for mainland China or cross-border connectivity. Software availability does not provide regulatory approval or guarantee anonymity.

## Features and limits

- Manage SOCKS5 nodes and L2TP mappings, bulk imports and start/stop actions.
- Restore mappings saved as `running` after service restart by default.
- Measure upstream download/upload throughput and connection latency from the server.
- Manage web credentials, HTTPS/SSH ports, root login password and optional L2TP/IPsec.

Tunnel operation requires Linux, systemd, root privileges, TUN support, xl2tpd, PPP, sing-box and iptables. The installer supports Ubuntu/Debian on amd64 and arm64; Ubuntu Server 24.04 LTS amd64 is the recommended baseline, not a guarantee for every host. Non-Linux builds use platform stubs and cannot provide the tunnel service. The management UI supports English, Traditional Chinese and Simplified Chinese with session-based sign-in. Installer messages support the same languages through `--lang en|zh-Hant|zh-Hans` (default: `zh-Hans`).

**Plain L2TP is unencrypted and is the default.** Management HTTPS does not encrypt tunnel traffic. Optional IPsec uses IKEv1 with a shared PSK; validate client compatibility and your security policy. SOCKS5 alone provides no transport encryption. UDP capability depends on the upstream. PPP IPv4 forwarding is restricted to assigned TUNs and PPP IPv6 is blocked; validate client routing and firewall compatibility. No anonymity guarantee is provided.

Persistent PPP leak guards, separate upstream TCP/UDP/DNS health monitoring, and automatic sing-box/TUN recovery are described in the [operations guide](docs/en/operations.md). Existing installations must install the new guard service before restarting xl2tpd.

## Installation

Use a dedicated host: installation changes packages, forwarding, PPP/L2TP configuration, firewall rules and system services. Keep security updates enabled. Review the scripts before running them as root. The one-command installer selects the latest stable release by default; set `BRANCH` explicitly for a reviewed development branch. It is intended for a new installation.

```bash
curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_install.sh | sudo bash -s -- --lang en
```

Allow the printed HTTPS TCP port only from trusted administrator IPs. The installer generates a 365-day self-signed certificate and random 16-character admin password, and binds the backend to `127.0.0.1:8080`. Verify the printed SHA-256 certificate fingerprint through a trusted channel before trusting the certificate. Without `HTTPS_PORT`, it selects an unused port in 10000–60000; 8080 is reserved. Public IPv4 is detected automatically; set `PUBLIC_IP` when necessary.

To choose a fixed HTTPS port instead of the random default, use `HTTPS_PORT`, for example 8443. Allow that port before accessing the management interface:

```bash
curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_install.sh | sudo env HTTPS_PORT=8443 bash -s -- --lang en
```

Open the printed HTTPS URL, add an authorized SOCKS5 node, create a mapping and start it to `running`. Configure the client with the server IP and mapping's L2TP credentials. Plain L2TP needs UDP 1701; optional IPsec needs UDP 500/4500 with enforced NAT-T. Read the operations guide before exposing tunnel ports. Do not rerun the installer to upgrade. For installation from a reviewed source checkout, see the [development guide](docs/en/development.md).

## Upgrade

For an existing default installation, use the one-command updater. It builds first, backs up the deployment, preserves settings, and installs the required guard service. Running clients must reconnect; stopped services remain stopped. See the [operations guide](docs/en/operations.md) for failure recovery and manual alternatives.

```bash
curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_update.sh | sudo bash
```

## Documentation

Uninstall: `sudo s2l uninstall` or menu **13**. Preview with `sudo s2l uninstall --dry-run`. Confirmation is required; S2L data, clean default source, backups and verified dedicated dependencies are removed; managed VPN sessions disconnect. See the [uninstall procedure](docs/en/operations.md#uninstall).

| Topic | English | 繁體中文 | 简体中文 |
|---|---|---|---|
| Operation, backup, updates, troubleshooting | [Guide](docs/en/operations.md) | [指南](docs/zh-Hant/operations.md) | [指南](docs/zh-Hans/operations.md) |
| Architecture, configuration, development | [Guide](docs/en/development.md) | [指南](docs/zh-Hant/development.md) | [指南](docs/zh-Hans/development.md) |
| Legal and compliance | [Notes](docs/en/legal.md) | [說明](docs/zh-Hant/legal.md) | [说明](docs/zh-Hans/legal.md) |

## Build and verification

Go 1.22 or later is required by `go.mod`; use a maintained compatible toolchain. Frontend builds require Node.js/npm compatible with the locked Vite dependencies.

```bash
npm --prefix web ci
npm --prefix web run build
go build -o bin/s2l ./cmd/s2l
go test ./...
python3 -m unittest discover -s script/tests
```

Commit `web/dist/` with frontend changes: it is embedded in the Go binary. See the development guide for safe test guidance.

## Security and licensing

Restrict management access, use HTTPS and protect credentials/backups. Keep a recovery SSH session during access changes. Never post configuration, state, PSKs or passwords in public issues. See [security reporting](SECURITY.md).

Original S2L code and documentation are licensed under [Apache-2.0](LICENSE). See [NOTICE](NOTICE) and [third-party notices](THIRD_PARTY_NOTICES.md); dependencies retain their own licenses. A software license does not grant regulatory approval. See the legal notes.

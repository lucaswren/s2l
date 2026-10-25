# Architecture and development

[Home](../../README.md) · [English](../en/development.md) · [繁體中文](../zh-Hant/development.md) · [简体中文](../zh-Hans/development.md)

[Client setup, DNS and acceptance checks](clients.md)

## Data path and responsibilities

An L2TP/PPP client connects to xl2tpd. S2L associates mapping credentials and an IPv4 subnet with a sing-box TUN interface, policy routing table and firewall rules. sing-box forwards mapped traffic to the selected SOCKS5 node. Nginx terminates management HTTPS and proxies to the Go HTTP backend; it is separate from the tunnel path.

| Path | Responsibility |
|---|---|
| `cmd/s2l/` | Startup, configuration validation, restore, shutdown |
| `internal/api/` | Session authentication for the UI and scripts, API validation and SPA delivery |
| `internal/service/` | Mapping lifecycle and rollback orchestration |
| `internal/singbox/`, `internal/network/`, `internal/l2tp/` | Process configuration, Linux routing/firewall, PPP/xl2tpd integration |
| `internal/store/`, `internal/model/`, `internal/config/` | Persistent state, models and runtime configuration |
| `internal/speedtest/`, `internal/diagnostics/` | Node probes, staged Google checks and address comparisons |
| `internal/dnsproxy/` | Per-mapping DoH/TCP resolver and scoped TCP preference |
| `internal/history/`, `internal/operations/`, `internal/debuglog/` | Bounded history, incidents, notifications and opt-in debug captures |
| `web/src/`, `web/src/i18n/`, `web/dist/` | Vue UI, language catalog and committed embedded assets |
| `script/`, `deploy/systemd/` | Privileged installation, management helpers and units |

Keep `web/dist/`, lockfiles, third-party notices and platform-specific Go implementations in the source distribution. `bin/`, `tmp/`, `.test-tmp/`, `web/node_modules/` and Python caches are local artifacts. Stop their test processes before cleaning them; preserve any diagnostic evidence still needed. Browser runners share `script/ui_test_helpers.mjs` to stop their own child processes and remove temporary profiles and backend data after each run, while retaining screenshots in `tmp/`.

## Configuration

Start with `config.example.json`; run `./bin/s2l -config /path/to/config.json` only on a suitable test host. Relative `data_dir` is resolved from the working directory; the installed service works in `/opt/s2l`.

Omitting `-config` uses defaults. An explicitly specified file must exist and contain a JSON object; a missing file or `null` stops startup instead of silently using defaults.

| Key | Default / purpose |
|---|---|
| `ui_language` | Optional saved administrator UI language; initially uses the browser language |
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

`make build` also builds the frontend; `make test` runs Go tests. `npm --prefix web run dev` starts Vite with an API proxy to localhost:8080. Non-Linux builds exercise stubs, not real Linux integration. Use a disposable authorized Linux VM for privileged integration checks. `make run` starts the local backend and can modify host networking and PPP files. `make install-service` delegates to `script/install.sh`, including dependencies, HTTPS and service setup; run it only on a host intended for a fresh installation. Existing installations use `script/update.sh`.

Keep changes focused, explain their trigger and behavior, and include relevant validation. Never commit runtime configuration, state, dependencies or secrets. Commit rebuilt `web/dist/` with frontend edits. Documentation should be updated in English first, then Traditional Chinese, then Simplified Chinese; retain matching commands, defaults and legal meaning. Link official legal sources with a check date. Contribute only content you have the right to submit under Apache-2.0; retain third-party notices. Run `python3 script/collect_frontend_licenses.py` after dependency updates and commit the generated inventory.

Use **S2L** as the display name in documentation, UI labels and service descriptions. Keep executable names, module/repository paths, commands, configuration keys and existing machine-recognized markers lowercase (`s2l`). Use SOCKS5, L2TP and IPsec consistently; distinguish server-side probes from client-path measurements and rollback attempts from verified recovery.

Chinese editions use the script-based tags `zh-Hant` (繁體中文) and `zh-Hans` (简体中文), following [BCP 47 script subtags](https://www.rfc-editor.org/rfc/rfc5646.html#section-2.2.3). These tags identify the writing system; legal jurisdiction is stated separately in the content. Keep filenames, navigation links and HTML language attributes consistent. Run `python3 script/check_docs.py` to check local documentation links before submitting changes.


## Diagnostic integration tests

```bash
# Build web/dist first. Requires local Chrome, Node.js 22+ and Python 3.
node script/test_ui.mjs
node script/test_auth_ui.mjs

# Disposable Linux host only: requires PPP/L2TP kernel support and test dependencies.
go test -c -o /tmp/s2l-l2tp.test ./internal/service
sudo python3 script/test_l2tp_integration.py --test-binary /tmp/s2l-l2tp.test --singbox /absolute/path/to/sing-box
for transport in ipsec ipsec-nat ipsec-shared-nat ipsec-shared-nat-1701; do
  sudo python3 script/test_l2tp_integration.py --test-binary /tmp/s2l-l2tp.test --singbox /absolute/path/to/sing-box --transport "$transport"
done
```

The L2TP runner isolates mount, network and PID namespaces. It negotiates DNS through real PPP IPCP, checks custom DoH and same-node TCP DNS fallback for both UDP/TCP client queries, and verifies two fixed exits. Single and simultaneous upstream outages must fail client HTTPS and uncached DNS while keeping PPP connected. A separately reachable, certificate-verified HTTPS uplink is a positive control; an egress counter must remain zero throughout faults and TUN loss. The fixture also checks active UDP rejection, process recovery and missing-rule repair. Browser checks cover desktop and 390/320-pixel layouts, language switching and unsaved forms. The authentication runner uses the real backend with disposable data to test sign-in, session persistence and sign-out. These do not replace Linux data-plane tests.

Additional modes require strongSwan/starter, conntrack and kernel XFRM/conntrack-zone/NAT-INPUT support. `ipsec` tests clients without a NAT router (NAT-T is enforced), `ipsec-nat` uses separate translated addresses, `ipsec-shared-nat` shares an address with ports 1701/1702, and `ipsec-shared-nat-1701` shares both the public address and source port 1701. All use production configuration and the updown helper, checking DNS, per-node fixed exits, UDP allow/reject-quic, upstream/TUN failure and zero direct egress. The identical-port mode deliberately delays updown to exercise early packets, repairs the firewall with both SAs active, removes a response mark, rekeys a CHILD_SA and disconnects/redials one client while verifying the other remains usable and the retired conntrack zone is empty. Missing inbound/outbound guards must be detected and repaired. External node probes are separate; Linux peers do not establish native Windows/macOS compatibility.

The identical-port test also forces the allocation high-water counter to 65,536 and performs four recycling rounds (seven additional real redials, including three concurrent two-client rounds). It uses the actual 30-second quarantine and lets production updown reclaim the endpoints automatically. A retained UDP socket and a failed control query must prevent reuse; stale replies must hit the output guard. The other client is continuously probed while one endpoint retires/reconnects. DNS, UDP policy, fixed SOCKS identities and ESP are checked after each round, followed by the existing upstream/TUN outage and zero-direct-egress checks. Python regressions cover 16 concurrent processes allocating both fresh and recycled slots, 45,540 simulated lifetimes, incomplete/changed global snapshots, half-open tunnels, crash-recoverable journals and inspection failures. These long-loop kernel observations are mocked; they do not represent 45,540 real VPN dials.

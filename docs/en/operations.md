# Operations guide

[Home](../../README.md) · [English](../en/operations.md) · [繁體中文](../zh-Hant/operations.md) · [简体中文](../zh-Hans/operations.md)

Read the [legal notes](legal.md) and README security limits first. Keep security updates enabled; wait for package-manager locks instead of routinely disabling unattended updates. Use maintenance windows for changes that interrupt connections.

## New installation

Use the one-command installer on a new, dedicated Ubuntu/Debian host after reviewing the scripts:

```bash
curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_install.sh | sudo bash
```

It runs as root and follows `main` by default. HTTPS uses a random available TCP port unless `HTTPS_PORT` is supplied. Restrict the printed management port to trusted administrator IPs. See the README for fixed-port configuration and the development guide for installation from a reviewed source checkout. Use the update procedure below for existing installations.

## Upstream health and leak protection

Mapping `running` and upstream health are separate. The monitor reports TCP, UDP and DNS independently:

| Check | Actual probe | Interpretation |
|---|---|---|
| TCP | A small HTTPS response through SOCKS5 authentication and forwarding; Cloudflare and Google are alternate targets | Successful web forwarding to a probe target |
| UDP | SOCKS5 `UDP ASSOCIATE`, followed by a DNS datagram to `1.1.1.1:53` or `8.8.8.8:53` through the negotiated relay | A valid reply must match its source, query ID and question; fragmented/invalid replies are ignored. Only explicit SOCKS reply code 7 is classified as unsupported |
| DNS | A validated DNS response from `https://1.1.1.1/dns-query` through SOCKS5; if that fails, a TCP DNS probe to `8.8.8.8:53` through SOCKS5 | Working primary DoH is healthy. If only TCP DNS succeeds, the UI reports backup-only availability; this probe does not control live DNS; automatic mode switches based on real query failures |

DNS probes query the public name `example.com`. Target traffic always uses the selected proxy; there is no direct DNS-target fallback. Proxy/relay hostnames may still require the server's own resolver to establish the upstream connection. Providers receive these probe connections and queries. The health checker does not read or upload client DNS history.

An initial protocol failure is unstable; three consecutive failures are unavailable. Healthy recovery needs two consecutive successful checks. Explicit UDP non-support and DNS backup-only availability are reported separately. Working TCP with a UDP/DNS problem is partial availability, rather than a whole-node outage; remaining successful checks also prevent a blanket unavailable result. L2TP stays connected and these protocol failures do not trigger a sing-box restart.

Normal checks run about every 10 seconds plus probe duration and scheduling. TCP failures back off by 5, 10, 20, 40 and at most 60 seconds; partial availability may retain the normal interval. The monitor handles at most four mappings concurrently, with three protocol probes in parallel per mapping and a 12-second overall probe deadline. These tests do not verify the local TUN/L2TP client path, every DNS name, DNSSEC, QUIC or every UDP application.

L2TP remains connected during upstream outages. Failed application connections need to reconnect. A missing sing-box process or TUN triggers bounded-rate recovery without stopping L2TP. Recovery failures appear in health status and service logs. No automatic switch to another node is performed; change a mapping's node after stopping it. Connection settings of nodes used by running mappings cannot be edited.

S2L requires a dedicated host: its persistent guard covers **all `ppp+` interfaces**. IPv4 forwarding from PPP is permitted only for a configured source subnet toward its assigned TUN; other forwarding and all PPP access to the host are dropped. PPP IPv6 is blocked. A terminal unreachable route prevents main-table fallback when the TUN disappears. Guards remain after stopping S2L or a mapping so existing PPP sessions cannot escape via the host uplink. `s2l-guard.service` installs them before xl2tpd starts; failure prevents xl2tpd/S2L startup. Do not flush these rules or run another firewall manager that replaces them. Client-side split routing and other network adapters remain outside this server's control.

## Upgrade

For an existing default `/opt/s2l` installation, run:

```bash
curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_update.sh | sudo bash
```

The updater downloads and builds before stopping services, then creates a private backup in `/root/s2l-backup.*`, installs the application, management scripts and service units, and verifies the local authenticated API when S2L was running. Configuration, accounts, nodes, certificates and ports are preserved; services that were stopped remain stopped. Running clients must reconnect.

If an upgrade fails after stopping services, it attempts to restore backed-up files and keeps PPP services stopped with guards in place. This is not an automatic return to service: inspect the printed backup path and logs, then retry or recover manually. Interrupted downloads/builds leave services running. The script does not change Nginx, SSH, the Go toolchain or sing-box. It requires the standard file paths and a local backend listener; custom deployments use the manual procedure.

The remote command follows `main` and executes downloaded code as root. Review it before use; set `S2L_REF` to a reviewed commit using `sudo env S2L_REF=COMMIT bash` instead of `sudo bash`. After the first upgrade, `s2l 11` invokes the same updater. From a reviewed local checkout, use `sudo bash script/update.sh`.

### Manual upgrade

For an existing installation, build the reviewed version, stop **both S2L and xl2tpd**, back up the old deployment, and replace the application binary. Install the new units from the reviewed checkout before restarting (the following assumes the default `/opt/s2l` path):

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


## Access and client setup

The installation prints the HTTPS URL, username and initial password. Restrict the management TCP port to trusted source IPs in both host firewall and cloud security groups. Keep backend TCP 8080 private. Add an authorized SOCKS5 node and an L2TP mapping, then start it. A `running` status is not an end-to-end connectivity or security test: verify the intended egress, DNS, IPv6 handling and failure behavior from an actual client.

| Mode | Network access | Protection |
|---|---|---|
| Management | Selected HTTPS TCP port | TLS for web/API only |
| Plain L2TP (default) | UDP 1701 from authorized clients | No tunnel encryption |
| L2TP/IPsec | UDP 500/4500 and ESP, protocol 50 | IKEv1/shared PSK; test NAT and client compatibility |

Prefer encrypted client transport where compatible with your security policy. In System Settings, enable IPsec with a PSK of at least 12 characters; all mappings share it. A blank PSK retains the existing key. The server blocks plain L2TP when IPsec is enabled; disabling IPsec requires clearing the client PSK. Reconnect clients after mode/key changes. Failures in IPsec configuration/firewall changes trigger a rollback attempt; the helper refuses detected conflicts with existing IPsec services. Backup metadata: `/opt/s2l/ipsec-backup.json`.

Node speed tests use Cloudflare through the selected SOCKS5 upstream, transferring 5 MB per direction by default. They consume bandwidth and do not measure the full L2TP client path; one direction's failure can coexist with the other's successful result.

## Management and credentials

Run `s2l` for the interactive menu, or `s2l NUMBER` for one action. Unprivileged users are prompted for sudo.

| Number | Action |
|---|---|
| 1 | URL and username, without password |
| 2 / 3 | Service status / restart |
| 4 | Last 80 log entries |
| 5 / 6 | Start / stop service |
| 7 / 8 | Change admin password / username |
| 9 / 10 | Change management port / SSH port |
| 11 | Upgrade S2L, preserving configuration |
| 0 | Exit menu |

Admin passwords are 12–128 characters; the installer/CLI allow a restricted character set. CLI account changes restart S2L and may interrupt tunnels; web account changes take effect without a service restart. Log in again afterwards. The CLI uses `public_url` when configured; otherwise it derives the address from the listener and detected IP, with a NAT warning on fallback.

Before changing any access port, allow the new port first. Verify the new URL or SSH connection before closing the old session. SSH changes validate `sshd` configuration and listening state, with rollback attempts on failure, and support Ubuntu `ssh.socket`; backup metadata is `/opt/s2l/ssh-port-backup.json`. Web HTTPS port changes reload Nginx, preserve the backend and certificate, and attempt rollback after validation/reload failures.

The web root-password action requires authenticated HTTPS access. It accepts 12–128 visible ASCII characters or generates a 16-character password. It changes the Linux root password, not web credentials; it does not enable SSH password authentication or unlock the account. Retain the original SSH session and verify access separately.

## HTTPS certificates

For an existing installation, review and run from the checkout after allowing the selected HTTPS port:

```bash
sudo bash script/setup_https.sh YOUR_PUBLIC_IPV4 8443
s2l 1
```

Verify the printed SHA-256 fingerprint before trusting/importing `/opt/s2l/tls/self-signed.crt`. Do not share its private key. Self-signed certificates are not publicly trusted. No TCP 80 or public CA renewal is required by this setup. Before certificate expiry, rerun the helper; it regenerates when validity is under one day. Omitting the port on an existing HTTPS installation preserves its current port. Configuration failures trigger a restoration attempt. If recovery fails, inspect the saved backup and restore through SSH or a provider console.

## Backups and updates

Routing table IDs must be 1–252; Linux reserves 253–255. Assign only tables dedicated to S2L: cleanup flushes the selected table. Existing mappings using reserved IDs must be corrected before starting; do not manually flush system tables. Runtime files now live under private `/run/s2l` (0700), with 0600 logs. Install the updated service unit or let the root process create that directory. Old `/tmp/s2l-*` files from earlier versions are not automatically migrated; review and remove only verified stale project files after stopping old processes.

Installed configuration: `/opt/s2l/config.json`; installed state: `/opt/s2l/data/state.json`. Also protect TLS keys, PPP credentials, Nginx/systemd configuration and any SSH/IPsec files touched by helpers. Generated sing-box files/logs in `/run/s2l/s2l-*` may contain sensitive information. Restrict permissions and encrypt off-host backups. Do not upload them to public issues.

Before an update, record the deployed commit and versions, and take a consistent snapshot during a maintenance window with S2L stopped. Preserve configuration, state and the previous binary; also snapshot system networking/PPP/IPsec configuration. A binary-only rollback may not undo schema or system changes.

Build a reviewed revision in a separate checkout using the development guide. After verification, stop S2L, replace only the application binary with the built `bin/s2l`, and start the service. Review helper/unit changes separately before installing them. Do not use the new-install scripts as an updater: `install.sh` refuses existing configuration, and the remote script can reset its source checkout. Confirm mappings and actual client traffic after restart. If validation fails, stop the service and restore the tested snapshot/previous binary and matching state/configuration before restarting. Restore privileged files only with correct ownership and permissions.

## Troubleshooting

```bash
sudo systemctl status s2l xl2tpd nginx --no-pager
sudo journalctl -u s2l -n 80 --no-pager
sudo nginx -t
sudo ss -lntup
ip rule show
ip route show table all
```

- Web access fails: check the printed URL, firewall/security groups, Nginx and certificate fingerprint; do not expose the backend as a workaround.
- Mapping fails: inspect the mapping error, S2L journal and a log ZIP collected with Debug enabled in Settings; check TUN support, sing-box version and upstream reachability.
- Client cannot dial: check credentials, enabled tunnel mode, ports, NAT, xl2tpd and, when enabled, `s2l-ipsec` logs.
- Dial succeeds but traffic fails: check upstream, assigned subnet conflicts, policy routes, firewall and client DNS/route selection.
- SSH access changes fail: retain the current connection and use a provider console or approved recovery route; inspect backup metadata before restoring.

For failed Telegram, WeChat or web images, inspect the failed request and separate TCP/UDP/DNS health results. S2L preserves the client's destination IP instead of replacing CDN addresses with sniffed domains. By default, TCP SYN MSS is capped at 1200 in `mangle/FORWARD` for both PPP directions. Non-DNS UDP is actively rejected by default; DNS uses DoH/TCP. TCP fallback requires application support; UDP-only applications need a capable upstream. Automatic DNS mode retries failed DoH queries over TCP and prefers TCP after two primary failures. Primary recovery is checked in the background using at most one real query every 30 seconds; two successful checks restore DoH without restarting sing-box.

Updating restarts the service and automatically regenerates configurations for previously running mappings; reconnect L2TP and recreate application connections if needed. Inspect `s2l-tcpmss` with `sudo iptables -t mangle -S FORWARD`. If images still fail, compare the same failing image host through SOCKS5 and the client path, and inspect a log ZIP collected with Debug enabled in Settings to distinguish upstream, DNS and tunnel failures.

Sanitize logs before sharing. Do not flush all host firewall rules to diagnose a mapping problem. There is no documented automated full uninstall; removal must account for existing shared services and restore saved system configuration rather than blindly deleting PPP, Nginx or firewall files.


## Mapping options and Debug

Mappings accept MTU 1280–1400 (default 1400), MSS 536 through MTU-40 (default 1200), queue length 100–10000 (default 1000), UDP policy and DNS mode. Missing fields use compatible defaults; stop before editing. PPP interfaces are tuned by peer address during local checks; MRU remains 1400. Non-DNS UDP is rejected by default; UDP can optionally be allowed or rejected only on port 443. Allowing UDP does not add upstream capability.

Each mapping has a private loopback TCP resolver. Cloudflare DoH and Google TCP DNS both use the mapping's SOCKS5. A DNS-only outbound is pinned to that loopback endpoint; client traffic still uses SOCKS5. Valid NXDOMAIN does not trigger failover; malformed replies, SERVFAIL, REFUSED and timeout do. Both methods failing returns SERVFAIL, with no direct remote DNS fallback. sing-box owns caching. Live resolver counters and upstream probe results are displayed separately. Forced DoH and TCP modes do not switch transports; health checks also probe only the selected method.

The network test dialog supports a separate HTTPS host:port check; its results do not update automatic node health. Enable Debug, reproduce the real client issue, then inspect the timeline or export a ZIP. Debug is off by default: troubleshooting logs and resource samples are not retained while off. Node/mapping backups retain the latest 10 versions; stop all mappings before rollback. Local recovery allows three attempts per ten minutes, then a five-minute cooldown and a trial; two stable minutes reset the budget. See the [detailed stability notes (Simplified Chinese)](../../DEBUG-AND-STABILITY.zh-Hans.md).

Local liveness is scheduled every five seconds in a separate four-worker pool. Recovery backs off 5–60 seconds independently of upstream probes. Per-mapping locks serialize start/stop/delete/recovery; state reads do not wait on slow lifecycle locks. State edits still coordinate with lifecycle work, and stale probes are discarded.

Start/stop operations also serialize the shared xl2tpd configuration through the running-state commit, so simultaneous operations cannot overwrite another mapping's active configuration. Status reads and upstream probes remain independent. Text imports preserve existing MTU, MSS, queue, UDP and DNS options; JSON imports use submitted options. State writes use a unique temporary file, sync its contents and replace the original; failed writes restore in-memory state and remove the temporary file.

Credential/comment updates no longer restart xl2tpd. A real global config change while it is active is refused before overwriting the config; stop xl2tpd in a maintenance window and retry. New PPP sessions read credentials afresh; stopped mappings keep leak guards. Debug logs stay in bounded memory only and are cleared on S2L restart; sing-box log files are no longer created. Error categories refer to the current process's log lines rather than failed request totals. Exit counts persist for the lifetime of S2L; interface drops/errors and read failures are displayed.

## v1.3.1 diagnostics and upgrades

First startup backs up and migrates existing mappings to actively reject non-DNS UDP and use automatic DoH/TCP DNS. Stopped mappings stay stopped. Restoring business backups preserves the installed migration version. Upgrades wait for previously running mappings to recover locally; upstream outages alone do not fail verification. Initial restore failures use at most five delivery attempts per batch. Forwarding rules, NAT, policy routes and MSS are checked every 30 seconds and repaired without restarting a healthy process.

Unsupported or persistently unavailable UDP is retried every ten minutes; the first success triggers confirmation after ten seconds. Automatic probes share short-lived results for identical nodes and DNS modes; manual probes update monitoring. Debug includes protocol transitions, 256 recent connection observations and 20 failed-target summaries. Logs do not prove transfer success or original pre-NAT client identity.


## Layered diagnostics and monitoring history

Fixed SOCKS5 exits and the configured UDP policy remain unchanged. UDP capability is reported separately: an unsupported upstream does not downgrade a mapping configured to reject UDP. HTTPS diagnostics retain failure phase, HTTP status, elapsed time and per-target evidence; a completed 403/429/5xx response is a target restriction or service error rather than a proxy authentication failure.

Mapping forwarding tests create a temporary isolated peer and send HTTPS through the existing PPP ingress guard, policy route and TUN. They do not test L2TP authentication or alter permanent rules. Requires Linux, root, curl and a subnet of /29 or larger with a free diagnostic address; /30 mappings remain usable but cannot run this synthetic test. Node and mapping exit IPs are observed independently; differing IPs are not proof of misrouting when an upstream uses a dynamic exit pool. Tests can be cancelled, and stale diagnostic namespaces with dead owners are reclaimed at startup.

PPP sessions show interface, peer IP, actual MTU, counters and first observation time. This is not a count of computers behind a NAT gateway or the actual dial time. Failed collection retains the previous snapshot with a warning.

Operational history runs independently of Debug: 24 hours in five-minute buckets, saved approximately once per minute in `data/health-history.json`. Only completed probe samples are counted; absent samples are not treated as outages, and counters resetting cannot create traffic spikes. Exit change counts come only from successful mapping tests. No credentials or raw target URLs are saved in this history. Debug remains bounded and opt-in.

## Operational events and optional limits

Runtime collection is shared for five seconds; the API reports `collected_at`. PPP collection failures do not suppress independent firewall verification. TCP probes are shared by node credentials, DNS probes by node and DNS mode; UDP retains its separate low-frequency schedule.

Managed pppd up/down wrappers emit events through a root-only Unix socket and preserve distribution hooks. New sessions expose their connection time and can be individually disconnected after checking the interface, peer, PID and process identity. Sessions predating service startup retain the observation time and cannot be disconnected without verified events. An unavailable disconnect reason remains unknown.

The monitoring panel includes bounded incidents (500) and operations (1000), with node and multi-node correlation. Multi-node failures indicate a shared path to investigate, not a proven server-network cause. `operations.json` is private and includes optional notification credentials; protect it like other credentials. Notifications use administrator-configured HTTPS webhooks, with optional Bearer authorization, one-minute delivery cooldown, at most five delivery attempts per batch and maintenance suppression. Events remain local unless notifications are explicitly enabled.

Mapping upload/download limits are in Kbit/s; TCP connection limits apply to the whole mapping. Zero means unlimited. The optional PPP session cap rejects only newly connected excess sessions; duplicate accounts are flagged independently. Upload uses TBF shaping; download uses ingress policing and can drop excess traffic. These limits do not change the fixed upstream, DNS transport or UDP policy. Connection details are read on demand from each sing-box process over an authenticated loopback API, limited to 500 displayed entries, and are not stored in history.

Configuration forms preview changed field names without credential values and use resource revisions to reject stale edits. Active runtime settings still require a clean stop; existing automatic backups provide undo. Mapping groups filter batch operations. The authenticated `/api/metrics` endpoint exposes bounded operational gauges and counters without destination labels.

The optional transfer-quality test makes eight alternating bounded requests (1 KiB and 256 KiB), recording first-byte time, total duration, early EOF and median/P95. Targets may ignore Range; a bounded response is not a complete download or a path-MTU proof. Host TCP/conntrack statistics refer to the entire server.

Notification edits use a revision to reject stale forms. Changing the webhook address clears the old token unless a replacement is supplied. Unknown samples and stopped mappings are not reported as confirmed recovery.

## Google service diagnostics and comparisons

Use Network test → Google diagnostics on a node for manual checks of eight representative service hosts. Select a second fixed node, or import a previous exported Google JSON snapshot (up to 512 KiB) for historical comparison. Import never sends requests or changes configuration. Results remain in the dialog and can be exported.

DoH A and TCP DNS A are checked separately through each node, with AAAA queries as well. TCP, certificate-verified TLS, ALPN and HTTP checks pin each destination address. Failures trigger up to three attempts, interleaving DoH and TCP DNS addresses within the same node; individual errors are retained. HTTP/2 is negotiated when available; redirects are not followed and response reads are bounded. HTTP 401/403/404 and differing DNS addresses do not establish an IP ban. These independent resolvers do not measure the mapping's live resolver; AAAA results do not verify IPv6 forwarding.

No UDP/QUIC is sent, and fixed exits and UDP policies remain unchanged. A working TCP/TLS alternative does not prove that the client has fallen back. Validate client behavior and ICMP return through the access network separately. Node checks bypass mapping forwarding and its limits. The policy snapshot includes aggregate mapping TCP limits, PPP session caps and bandwidth limits (zero means unlimited), which do not identify device counts behind NAT. Export separate runs with a desktop online and offline to compare this symptom.

Read-only mapping UDP-reject and TCP-limit counters are sampled before and after diagnostics. Reproduce on the phone during the check. Counts are aggregate; unavailable samples, missing rules and resets are not reported as zero. A rule hit does not prove ICMP reached the phone.

## Management access from L2TP

The L2TP management access card is disabled by default. Configure an IPv4 actually assigned to the server, trusted PPP source CIDRs and explicit panel/SSH TCP ports. SSH effective configuration and the managed panel configuration are cross-checked against `ss -lnt`; port detection changes neither listeners nor authentication.

The exception applies only to local IPv4 INPUT from PPP, never FORWARD, fixed exits or UDP policy. IPv6 remains blocked. Rules are committed atomically with iptables-restore. Disabling or narrowing access may disconnect the current management session. Management authentication is required. A cloud NAT public IP not assigned locally requires separate address/path verification; this does not configure cloud firewalls, Nginx or SSH listeners. Permitted management traffic stays local instead of using SOCKS.


## Sign-in, languages and TCP preference

The management UI supports English, Traditional Chinese and Simplified Chinese, with browser-local language persistence. Form sign-in validates the existing administrator credentials on the backend; passwords are not stored in browser storage. Session cookies use HttpOnly and SameSite=Strict, plus Secure over HTTPS. Sessions expire after 12 hours or 30 idle minutes; sign-out, account changes and service restart invalidate them. Repeated login failures are temporarily throttled. Scripts use the same JSON sign-in endpoint and session cookie; upgrade verification signs in locally and discards the session afterward.

Management exceptions offer a list of local IPv4 addresses. The current panel IP is suggested only when confirmed local by the server; a sole local address is filled automatically, while multiple addresses require selection. Public NAT addresses need separate verification. Exceptions are never enabled automatically.

Under reject / reject-quic, the mapping resolver returns NOERROR with no answers for Google service HTTPS (type 65) queries, reducing DNS advertisement of HTTP/3. A/AAAA and DoH/TCP through the fixed node are unchanged. DNSSEC, signed, special or malformed queries bypass this policy, as do UDP-allow mappings and independent diagnostic resolvers. The counter is resolver.google_https_suppressed. This does not clear client Alt-Svc caches, control private App DNS, convert QUIC to TCP or prove client fallback. It also omits other optional HTTPS record metadata, including ECH hints; it is not a fix for TLS or account access failures.

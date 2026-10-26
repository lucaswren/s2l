# Operations guide

[Home](../../README.md) · [English](../en/operations.md) · [繁體中文](../zh-Hant/operations.md) · [简体中文](../zh-Hans/operations.md)

[Client setup, DNS and acceptance checks](clients.md)

Read the [legal notes](legal.md) and README security limits first. Keep security updates enabled; wait for package-manager locks instead of routinely disabling unattended updates. Use maintenance windows for changes that interrupt connections.

## New installation

Use the one-command installer on a new, dedicated Ubuntu/Debian host after reviewing the scripts:

```bash
curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_install.sh | sudo bash -s -- --lang en
```

Use only `--lang` to select installation messages: `en`, `zh-Hant`, `zh-Hans` (default). For example, append `-s -- --lang en` to `sudo bash`; from a source checkout use `sudo bash script/install.sh --lang en`. The selection applies to kernel checks, recovery and HTTPS setup. APT, Go and other external tools retain their original diagnostics. Repeat the same parameter after a required kernel reboot.

Nginx is installed with the initial dependencies so HTTPS setup does not repeat APT downloads. `[TIME]` records dependency installation, Go preparation, compilation, certificate work and readiness checks separately. The first Go build also compiles the standard library; package names show progress and later builds reuse Go’s cache. Compilation still depends on CPU, memory and disk speed.

Editing a SOCKS5 address, username or password now lists linked mappings that must be stopped before saving, including mappings in an error state awaiting cleanup. The form remains open; no mapping is stopped or restarted automatically. Name and group changes can be saved while mappings run. After stopping the listed mappings, save and start them as needed.

Ubuntu Server 24.04 LTS amd64 remains the recommended baseline. Debian 12 is a compatibility test target; switching distributions does not remove first-build or package-download costs. As of October 2026, [Debian 12 is in LTS through June 2028](https://www.debian.org/releases/bookworm/); [Debian 13 has a longer support runway](https://www.debian.org/releases/trixie/) but has not passed this project’s full VM tests.

It runs as root and selects the latest stable Release by default; an explicit `BRANCH` overrides that selection. HTTPS uses a random available TCP port unless `HTTPS_PORT` is supplied. Restrict the printed management port to trusted administrator IPs. See the README for fixed-port configuration and the development guide for installation from a reviewed source checkout. Use the update procedure below for existing installations.

The bootstrap skips its APT step when curl, Git, Python and the CA bundle are already available; the installer still checks and installs runtime dependencies. Invalid source paths/branches and active tunnel services are rejected before downloads. Existing source checkouts must have the requested origin and no local changes, including untracked files. Save those files or choose a new absolute `SRC_DIR`; the bootstrap does not reset them. Concurrent downloads use a separate lock.

### Choosing the server system

| Criterion | Ubuntu Server 24.04 LTS | Debian 12 |
|---|---|---|
| Project role | Recommended baseline | Supported compatibility target |
| Security support, October 2026 | Standard maintenance through May 2029 | In LTS through June 2028; coverage depends on package/architecture |
| Kernel maintenance | GA or HWE; block known L2TP regressions before installation | 6.1 family; use an image with PPP/L2TP/TUN modules |
| Test evidence | Full VM install, reboot, HTTPS recovery and real PPP tests | The same full VM tests; ARM64 is build-only for both systems |

See [Ubuntu's lifecycle](https://ubuntu.com/about/release-cycle) and [Debian's release status](https://www.debian.org/releases/bookworm/). A distribution switch is not a fix for shared VPN identities, router policies or SOCKS5 outages. One controlled test measured cold compilation at 22s/18s and certificate work at 0–1s on Ubuntu/Debian respectively; this is not a general performance benchmark. Slow installation should be diagnosed from `[TIME]`, APT locks/downloads and machine resources. On an existing server, keep the current OS and schedule kernel/security maintenance separately.

### Firewall ownership and required ports

S2L owns its PPP/TUN forwarding guards, not the host's complete public firewall. An INPUT policy of ACCEPT does not restrict public SSH or HTTPS. Use provider security groups or reviewed host rules for management access. Do not change the live management path without a verified recovery console.

| Traffic | Required allowance |
|---|---|
| Administration | Existing SSH TCP port and selected HTTPS TCP port, restricted to administrator sources |
| Plain L2TP | UDP 1701 from intended clients |
| L2TP/IPsec | UDP 500/4500; ESP when not UDP-encapsulated; UDP 1701 only under the configured IPsec policy |
| Backend | 127.0.0.1:8080 only; no public allowance |

Health checks detect S2L entry hooks shadowed by earlier rules and restore their priority without flushing foreign rules. Each address family's filter change is committed together and PPP management exceptions remain first. This is periodic repair, not protection against a continuously competing firewall manager or separate native nftables base chains. Keep one consistent iptables backend and avoid concurrent rule replacement. Generated PPP options omit proxy ARP because client subnets are routed through TUNs, not bridged onto the host LAN; existing sessions adopt option changes only after reconnecting.

## Upstream health and leak protection

TUN replies are accepted only for established or related connections returning through PPP to the mapping's client subnet. If PPP disconnects and the client route disappears, late replies are dropped before reaching the default uplink. Each managed TUN's return hook and terminal drop survive mapping teardown until host reboot; stopping a mapping removes its allowance, and repeated redials on the same interface do not accumulate rules. Upgrades remove legacy broad allowances, and health repair restores missing return protection in place.

Mapping `running` and upstream health are separate. The monitor reports TCP, UDP and DNS independently:

| Check | Actual probe | Interpretation |
|---|---|---|
| TCP | A small HTTPS response through SOCKS5 authentication and forwarding; Cloudflare and Google are alternate targets | Successful web forwarding to a probe target |
| UDP | SOCKS5 `UDP ASSOCIATE`, followed by a DNS datagram to `1.1.1.1:53` or `8.8.8.8:53` through the negotiated relay | A valid reply must match its source, query ID and question; fragmented/invalid replies are ignored. Only explicit SOCKS reply code 7 is classified as unsupported |
| DNS | A validated DNS response from the configured DoH endpoint through SOCKS5; auto mode falls back to the configured TCP DNS endpoint. Defaults: `https://1.1.1.1/dns-query` and `8.8.8.8:53` | Working primary DoH is healthy. If only TCP DNS succeeds, the UI reports backup-only availability; this probe does not control live DNS; automatic mode switches based on real query failures |

DNS probes query the public name `example.com`. Target traffic always uses the selected proxy; there is no direct DNS-target fallback. Proxy/relay hostnames may still require the server's own resolver to establish the upstream connection. Providers receive these probe connections and queries. The health checker does not read or upload client DNS history.

An initial protocol failure is unstable; three consecutive failures are unavailable. Healthy recovery needs two consecutive successful checks. Explicit UDP non-support and DNS backup-only availability are reported separately. Working TCP with a UDP/DNS problem is partial availability, rather than a whole-node outage; remaining successful checks also prevent a blanket unavailable result. L2TP stays connected and these protocol failures do not trigger a sing-box restart.

Healthy checks run every 45–55 seconds plus probe duration and scheduling, with faster initial/failure confirmation. TCP failures back off by 5, 10, 20, 40 and at most 60 seconds; partial availability may retain the normal interval. The monitor handles at most four mappings concurrently, with separately bounded protocol probes in parallel per mapping. Mappings rejecting UDP skip automatic UDP probes; unavailable or unsupported UDP uses a ten-minute retry interval. These tests do not verify the local TUN/L2TP client path, every DNS name, DNSSEC, QUIC or every UDP application.

L2TP remains connected during upstream outages. Failed application connections need to reconnect. A missing sing-box process or TUN triggers bounded-rate recovery without stopping L2TP. Recovery failures appear in health status and service logs. No automatic switch to another node is performed; change a mapping's node after stopping it. Connection settings of nodes used by running mappings cannot be edited.

S2L requires a dedicated host: its persistent guard covers **all `ppp+` interfaces**. IPv4 forwarding from PPP is permitted only for a configured source subnet toward its assigned TUN; other forwarding and PPP access to the host are dropped, except explicitly configured local management access. PPP IPv6 is blocked. A terminal unreachable route prevents main-table fallback when the TUN disappears. Guards remain after stopping S2L or a mapping so existing PPP sessions cannot escape via the host uplink. `s2l-guard.service` installs them before xl2tpd starts; failure prevents xl2tpd/S2L startup. Do not flush these rules or run another firewall manager that replaces them. Client-side split routing and other network adapters remain outside this server's control.

## Kernel preparation and manual upgrade

Fresh installation checks both the running kernel and the highest installed generic kernel under `/boot`. Ubuntu `6.8.0-146-generic` and `6.8.0-147-generic` are rejected for the known PPPoL2TP regression (2169477). With the default `S2L_KERNEL_UPGRADE=auto`, a dedicated fresh Ubuntu 24.04 host with a standard GRUB default can prepare `linux-generic-hwe-24.04` from its configured APT repositories. Existing installations, active PPP/tunnel services, containers and custom boot selections are refused before kernel preparation. Other distributions/releases need manual handling when affected. Unlisted kernels are not necessarily tested kernels.

Preparation checks free space, package configuration, image/initramfs, offline PPP/L2TP/TUN module availability and the GRUB entry. It preserves old kernels, suppresses needrestart-driven service restarts and never reboots. **Exit 20** means the kernel is prepared but S2L is not installed. Arrange a maintenance window and console recovery, reboot manually, verify `uname -r`, then repeat the original install command with the same parameters. Repeating before reboot reuses complete kernel artifacts. There is no automatic resume service or saved password/parameter file. Kernel packages remain outside S2L installation rollback. `S2L_KERNEL_UPGRADE=off` disables preparation but does not bypass the known-regression check.

For an existing server with this feature installed, run one command (or select menu **12**):

```bash
sudo s2l kernel-upgrade
```

It checks Ubuntu 24.04 generic kernel support, disk space, package state and standard GRUB defaults, saves boot settings under `/var/backups/s2l-kernel-*`, installs HWE and verifies boot files and PPP/L2TP/TUN modules. Old kernels remain installed. The helper does not restart services or change S2L configuration. Repeating it reuses a complete pending kernel.

After it reports the new kernel ready, verify provider-console access and run `sudo reboot` separately during a maintenance window. Reconnect, check `uname -r` and test actual L2TP connectivity. There is no automatic reboot; the server reboot disconnects SSH/L2TP even while local iWAN stays running. S2L does not need reinstallation. With an older CLI, run `sudo python3 script/manage_kernel.py --upgrade` from a reviewed checkout containing this feature. Unsupported systems or custom boot selections require manual handling; do not reboot after a failed preparation. Package verification is not an actual dial test.

<details>
<summary>Manual commands and recovery details</summary>

For an existing Ubuntu 24.04 server, use this procedure rather than the fresh installer. First take a provider snapshot, save S2L/PPP/TLS/GRUB configuration and verify an out-of-band console. A kernel switch disconnects SSH/L2TP even if the local iWAN client keeps running. In a root shell, inspect `uname -r`, `/etc/os-release`, `df -h / /boot` and `dpkg --audit`. Proceed only with sufficient space and no incomplete package operations:

```bash
export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l
apt-get -o DPkg::Lock::Timeout=120 -o Acquire::Retries=2 update
apt-cache policy linux-generic-hwe-24.04
apt-get -s --no-remove --no-install-recommends install linux-generic-hwe-24.04 kmod
# Review the simulation before installing. No reboot is requested here.
apt-get -o DPkg::Lock::Timeout=120 -o Acquire::Retries=2 \
  install -y --no-remove --no-install-recommends linux-generic-hwe-24.04 kmod
dpkg-query -W -f='${Depends}\n' linux-image-generic-hwe-24.04
```

Use the actual image version from the last command to inspect `/boot/vmlinuz-VERSION`, `/boot/initrd.img-VERSION` and `modinfo -k VERSION` for `tun`, `ppp_generic`, `l2tp_ppp`; do not load the new modules into the running kernel. Run `update-grub` and confirm the intended default boot entry. A `saved` or custom entry can keep the old kernel selected. A hidden menu with zero timeout needs a usable console recovery path; after backing up GRUB settings, `GRUB_TIMEOUT_STYLE=menu` and `GRUB_TIMEOUT=8` in a new `/etc/default/grub.d/99-s2l-maintenance.cfg` can expose the menu. Do not overwrite an existing custom file. Regenerate GRUB after any change.

The [official HWE track](https://ubuntu.com/kernel/lifecycle) supplies supported kernels, but package availability is not an L2TP compatibility test: validate dialing/redialing in isolation first. Do not run a full distribution upgrade or remove old kernels as part of this task. Reboot separately during the agreed outage window, then verify `uname -r`, `systemctl is-active s2l xl2tpd nginx`, actual client DNS/egress and `journalctl -k -b` for refcount/use-after-free/lockup errors. If boot/network fails, select a previously validated kernel from GRUB Advanced options via the provider console; do not use 146/147 as a stable fallback. Old kernels are temporary recovery options. One-shot boot selection does not guarantee an automatic reboot after a hang.

</details>

## Upgrade

For an existing default `/opt/s2l` installation, run:

```bash
curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_update.sh | sudo bash
```

Before building or stopping S2L/PPP/IPsec services, the updater installs `conntrack` if missing using `apt-get` on managed Ubuntu/Debian hosts. It bounds package-lock waiting and downloads, skips this step when the command already exists, and does not request an unrelated package upgrade. If dependency preparation fails, it exits before changing application files or stopping services; fix the package source/lock or install `conntrack`, then retry. Any packages already installed are retained.

The updater downloads and builds before stopping services, then creates a private backup in `/root/s2l-backup.*`, installs the application, management scripts and service units, and verifies the local authenticated API when S2L was running. Configuration, accounts, nodes, certificates and ports are preserved; services that were stopped remain stopped. Running clients must reconnect.

If an upgrade fails after stopping services, it attempts to restore backed-up files and keeps PPP services stopped with guards in place. This is not an automatic return to service: inspect the printed backup path and logs, then retry or recover manually. Interrupted downloads/builds leave services running. The script does not change Nginx, SSH, the Go toolchain or sing-box. It requires the standard file paths and a local backend listener; custom deployments use the manual procedure.

The remote command follows `main` and executes downloaded code as root. Review it before use; set `S2L_REF` to a reviewed commit using `sudo env S2L_REF=COMMIT bash` instead of `sudo bash`. After the first upgrade, `s2l 11` invokes the same updater. From a reviewed local checkout, use `sudo bash script/update.sh`.

### Reviewed source and custom deployments

For the default managed layout, run `sudo bash script/update.sh` from the reviewed checkout. This keeps the binary, management helpers (including `ipsec_nat.py`) and systemd units in sync. A binary-only replacement does not install the new IPsec dependencies or hooks. Custom deployments must adapt the complete updater: stop xl2tpd and S2L before stopping IPsec, preserve configuration and credentials, install the matching helpers/units and guards, then restore the prior service states.


## Access and client setup

The installation prints the HTTPS URL, username and initial password. Restrict the management TCP port to trusted source IPs in both host firewall and cloud security groups. Keep backend TCP 8080 private. Add an authorized SOCKS5 node and an L2TP mapping, then start it. A `running` status is not an end-to-end connectivity or security test: verify the intended egress, DNS, IPv6 handling and failure behavior from an actual client.

| Mode | Network access | Protection |
|---|---|---|
| Management | Selected HTTPS TCP port | TLS for web/API only |
| Plain L2TP (default) | UDP 1701 from authorized clients | No tunnel encryption |
| L2TP/IPsec | UDP 500/4500; NAT-T enforced | IKEv1/shared PSK; test NAT and client compatibility |

Prefer encrypted client transport where compatible with your security policy. In System Settings, enable IPsec with a PSK of at least 12 characters; all mappings share it. A blank PSK retains the existing key. The server blocks plain L2TP when IPsec is enabled; disabling IPsec requires clearing the client PSK. Reconnect clients after mode/key changes. Failures in IPsec configuration/firewall changes trigger a rollback attempt; the helper refuses detected conflicts with existing IPsec services. Backup metadata: `/opt/s2l/ipsec-backup.json`.

The owned INPUT and OUTPUT chains are checked for exact rules and safe ordering. Reapplying IPsec or restarting its service repairs both guards atomically. OUTPUT drops L2TP replies without an IPsec policy, including failures of the per-SA NAT helper. Shared-NAT clients may keep source port 1701; see [client requirements](clients.md).

The updown helper journals its own rules under `/run/s2l-ipsec-nat`, locks concurrent notifications and deletes only the retired SA's conntrack entries. Service stop/start cleans stale journaled rules. Incoming decrypted L2TP is admitted only after that SA's isolation rules are ready; early packets are dropped before conntrack confirmation, preventing redial from caching an untranslated peer. Static firewall repair preserves active admission rules.

Reserve conntrack zones 20000–65535 for S2L on this dedicated host; internal peer ports are not public listeners and must not be opened in a security group. Retired endpoints enter a reusable pool rather than permanently consuming a 45,536-session lifetime budget. New SA notifications automatically collect eligible endpoints under the same process lock used for allocation. Retirement removes admission/routing rules and the old zone's connections, then waits at least 30 seconds. Two empty observations at least two seconds apart must account for all xl2tpd tunnels (including half-open tunnels), match stable global/LNS snapshots, and find no old endpoint socket, queued L2TP packets, associated XFRM state/policy or zone entries. XFRM generations are identified by reqid/marks plus a retirement snapshot of unmarked inbound SPIs; this avoids confusing a later SA that reuses the reqid with the retired SA. Only SPI/address metadata is journaled, never key material. Online clients keep their original slots and SOCKS5 mappings.

The 45,536 limit now applies to active plus not-yet-safe endpoints, not cumulative successful reconnects. A full pool waits for the short second observation when safe candidates exist; if safety cannot be established, new sessions fail closed. Inspection failures or unsupported xl2tpd status responses preserve reservations and record `reclaim_error` in `/run/s2l-ipsec-nat/state.json`; they never force reuse. This has been tested with xl2tpd 1.3.18. The helper requires the root-owned `/run/xl2tpd/l2tp-control` interface and complete global/LNS status support; custom LAC configurations are conservatively excluded. Reclamation is demand-driven, so idle retired records may remain until the next SA. Do not delete the runtime journal. Legacy journals without retired identities retain their old prefix until full-stack cleanup; the normal updater already stops xl2tpd and IPsec before starting them in order, safely migrating that state.

The updater preserves the IPsec enabled/running state and refreshes generated settings on service startup. Missing `conntrack` is installed automatically as described in the upgrade section.

On Ubuntu hosts with the packaged charon AppArmor profile, IPsec startup adds only read permission for `/opt/s2l/ipsec.secrets` to `/etc/apparmor.d/local/usr.lib.ipsec.charon` and reloads the profile. Other administrator rules and enforcement are retained. An unsupported local profile or reload failure prevents IPsec startup; the narrow read rule remains for retries and older deployed versions.

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
| 12 / `kernel-upgrade` | Prepare Ubuntu 24.04 HWE kernel without rebooting |
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

Routing table IDs must be 1–252; Linux reserves 253–255. Assign only tables dedicated to S2L: cleanup removes only exact S2L-owned routes, and startup rejects foreign table contents. Existing mappings using reserved IDs must be corrected before starting; do not manually flush system tables. Runtime files now live under private `/run/s2l` (0700), with private generated configurations; sing-box logs stay in bounded memory. Install the updated service unit or let the root process create that directory. Old `/tmp/s2l-*` files from earlier versions are not automatically migrated; review and remove only verified stale project files after stopping old processes.

Installed configuration: `/opt/s2l/config.json`; installed state: `/opt/s2l/data/state.json`. Also protect TLS keys, PPP credentials, Nginx/systemd configuration and any SSH/IPsec files touched by helpers. Generated sing-box configuration files in `/run/s2l/s2l-*` may contain sensitive information. Restrict permissions and encrypt off-host backups. Do not upload them to public issues.

Before an update, record the deployed commit and versions, and take a consistent snapshot during a maintenance window with S2L stopped. Preserve configuration, state and the previous binary; also snapshot system networking/PPP/IPsec configuration. A binary-only rollback may not undo schema or system changes.

Use the upgrade procedure above from a reviewed checkout; do not use the new-install scripts as an updater. Confirm mappings and actual client traffic after restart. If validation fails, keep PPP stopped and restore the complete tested snapshot, including matching helpers, units, binary and state/configuration. Restore privileged files with correct ownership and permissions.

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

Mappings accept MTU 1280–1400 (default 1400), MSS 536 through MTU-40 (default 1200), queue length 100–10000 (default 1000), UDP policy, DNS mode and custom DoH/TCP endpoints. Missing fields use compatible defaults; stop before editing. PPP interfaces are tuned by peer address during local checks; MRU remains 1400. Non-DNS UDP is rejected by default; UDP can optionally be allowed or rejected only on port 443. Allowing UDP does not add upstream capability.

Each mapping has a private loopback TCP resolver. DoH and TCP DNS both use the mapping's SOCKS5; defaults are Cloudflare DoH and Google TCP DNS. See [client setup and custom DNS](clients.md) for endpoint formats and PPP DNS advertisement. A DNS-only outbound is pinned to that loopback endpoint; client traffic still uses SOCKS5. Valid NXDOMAIN does not trigger failover; malformed replies, SERVFAIL, REFUSED and timeout do. Both methods failing returns SERVFAIL, with no direct remote DNS fallback. sing-box owns caching. Live resolver counters and upstream probe results are displayed separately. Forced DoH and TCP modes do not switch transports; health checks also probe only the selected method.

The network test dialog supports a separate HTTPS host:port check; its results do not update automatic node health. Enable Debug, reproduce the real client issue, then inspect the timeline or export a ZIP. Debug is off by default: troubleshooting logs and resource samples are not retained while off. Node/mapping backups retain the latest 10 versions; stop all mappings before rollback. Local recovery allows three attempts per ten minutes, then a five-minute cooldown and a trial; two stable minutes reset the budget. See the [detailed stability notes (Simplified Chinese)](../../DEBUG-AND-STABILITY.zh-Hans.md).

Local liveness is scheduled every five seconds in a separate four-worker pool. Recovery backs off 5–60 seconds independently of upstream probes. Per-mapping locks serialize start/stop/delete/recovery; state reads do not wait on slow lifecycle locks. State edits still coordinate with lifecycle work, and stale probes are discarded.

Start/stop operations also serialize the shared xl2tpd configuration through the running-state commit, so simultaneous operations cannot overwrite another mapping's active configuration. Status reads and upstream probes remain independent. Text imports preserve existing MTU, MSS, queue, UDP and DNS options; JSON imports use submitted options. State writes use a unique temporary file, sync its contents and replace the original; failed writes restore in-memory state and remove the temporary file.

Credential/comment updates no longer restart xl2tpd. A real global config change while it is active is refused before overwriting the config; stop xl2tpd in a maintenance window and retry. New PPP sessions read credentials afresh; stopped mappings keep leak guards. Debug logs stay in bounded memory only and are cleared on S2L restart; sing-box log files are no longer created. Error categories refer to the current process's log lines rather than failed request totals. Exit counts persist for the lifetime of S2L; interface drops/errors and read failures are displayed.

## Diagnostics and upgrades

First startup backs up and migrates existing mappings to actively reject non-DNS UDP and use automatic DoH/TCP DNS. Stopped mappings stay stopped. Restoring business backups preserves the installed migration version. Upgrades wait for previously running mappings to recover locally; upstream outages alone do not fail verification. Initial restore failures use the bounded local recovery budget: three attempts per ten minutes followed by a five-minute cooldown. Forwarding rules, NAT, policy routes and MSS are checked every 30 seconds and repaired without restarting a healthy process.

Unsupported or persistently unavailable UDP is retried every ten minutes; the first success triggers confirmation after ten seconds. Automatic probes share short-lived results for identical node credentials, DNS modes and endpoints; manual node probes update TCP/UDP health and only matching default-DNS health. Debug includes protocol transitions, 256 recent connection observations and 20 failed-target summaries. Logs do not prove transfer success or original pre-NAT client identity.


## Layered diagnostics and monitoring history

Fixed SOCKS5 exits and the configured UDP policy remain unchanged. UDP capability is reported separately: an unsupported upstream does not downgrade a mapping configured to reject UDP. HTTPS diagnostics retain failure phase, HTTP status, elapsed time and per-target evidence; a completed 403/429/5xx response is a target restriction or service error rather than a proxy authentication failure.

Mapping forwarding tests create a temporary isolated peer and send HTTPS through the existing PPP ingress guard, policy route and TUN. They do not test L2TP authentication or alter permanent rules. Requires Linux, root, curl and a subnet of /29 or larger with a free diagnostic address; /30 mappings remain usable but cannot run this synthetic test. Node and mapping exit IPs are observed independently; differing IPs are not proof of misrouting when an upstream uses a dynamic exit pool. Tests can be cancelled, and stale diagnostic namespaces with dead owners are reclaimed at startup.

PPP sessions show interface, peer IP, actual MTU, counters and first observation time. Session counts do not identify computers behind NAT. Without verified PPP events, first observation is not the actual dial time. Failed collection retains the previous snapshot with a warning.

Operational history runs independently of Debug: 24 hours in five-minute buckets, saved approximately once per minute in `data/health-history.json`. Only completed probe samples are counted; absent samples are not treated as outages, and counters resetting cannot create traffic spikes. Exit change counts come only from successful mapping tests. No credentials or raw target URLs are saved in this history. Debug remains bounded and opt-in.

## Operational events and optional limits

Runtime collection is shared for five seconds; the API reports `collected_at`. PPP collection failures do not suppress independent firewall verification. TCP probes are shared by node credentials, DNS probes by node credentials, DNS mode and endpoint addresses; UDP retains its separate low-frequency schedule.

Managed pppd up/down wrappers emit events through a root-only Unix socket and preserve distribution hooks. New sessions expose their connection time and can be individually disconnected after checking the interface, peer, PID and process identity. Sessions predating service startup retain the observation time and cannot be disconnected without verified events. An unavailable disconnect reason remains unknown.

The monitoring panel includes bounded incidents (500) and operations (1000), with node and multi-node correlation. Multi-node failures indicate a shared path to investigate, not a proven server-network cause. `operations.json` is private and includes optional notification credentials; protect it like other credentials. Notifications use administrator-configured HTTPS webhooks, with optional Bearer authorization, one-minute delivery cooldown, at most five delivery attempts per batch and maintenance suppression. Events remain local unless notifications are explicitly enabled.

Mapping upload/download limits are in Kbit/s; TCP connection limits apply to the whole mapping. Zero means unlimited. Fixed-address PPP accounts allow one session; only newly connected duplicates are rejected, including legacy zero limits. Upload uses TBF shaping; download uses ingress policing and can drop excess traffic. These limits do not change the fixed upstream, DNS transport or UDP policy. Connection details are read on demand from each sing-box process over an authenticated loopback API, limited to 500 displayed entries, and are not stored in history.

Configuration forms preview changed field names without credential values and use resource revisions to reject stale edits. Active runtime settings still require a clean stop; existing automatic backups provide undo. Mapping groups filter batch operations. The authenticated `/api/metrics` endpoint exposes bounded operational gauges and counters without destination labels.

The optional transfer-quality test makes eight alternating bounded requests (1 KiB and 256 KiB), recording first-byte time, total duration, early EOF and median/P95. Targets may ignore Range; a bounded response is not a complete download or a path-MTU proof. Host TCP/conntrack statistics refer to the entire server.

Notification edits use a revision to reject stale forms. Changing the webhook address clears the old token unless a replacement is supplied. Unknown samples and stopped mappings are not reported as confirmed recovery.

## Google service diagnostics and comparisons

Use Network test → Google diagnostics on a node for manual checks of eight representative service hosts. Select a second fixed node, or import a previous exported Google JSON snapshot (up to 512 KiB) for historical comparison. Import never sends requests or changes configuration. Results remain in the dialog and can be exported.

DoH A and TCP DNS A are checked separately through each node, with AAAA queries as well. TCP, certificate-verified TLS, ALPN and HTTP checks pin each destination address. Failures trigger up to three attempts, interleaving DoH and TCP DNS addresses within the same node; individual errors are retained. HTTP/2 is negotiated when available; redirects are not followed and response reads are bounded. HTTP 401/403/404 and differing DNS addresses do not establish an IP ban. These independent resolvers do not measure the mapping's live resolver; AAAA results do not verify IPv6 forwarding.

No UDP/QUIC is sent, and fixed exits and UDP policies remain unchanged. A working TCP/TLS alternative does not prove that the client has fallen back. Validate client behavior and ICMP return through the access network separately. Node checks bypass mapping forwarding and its limits. The policy snapshot includes aggregate mapping TCP limits, PPP session caps and bandwidth limits (zero is unlimited for TCP/bandwidth; fixed-address PPP accounts always allow one session), which do not identify device counts behind NAT. Export separate runs with a desktop online and offline to compare this symptom.

Read-only mapping UDP-reject and TCP-limit counters are sampled before and after diagnostics. Reproduce on the phone during the check. Counts are aggregate; unavailable samples, missing rules and resets are not reported as zero. A rule hit does not prove ICMP reached the phone.

## Management access from L2TP

The L2TP management access card is disabled by default. Configure an IPv4 actually assigned to the server, trusted PPP source CIDRs and explicit panel/SSH TCP ports. SSH effective configuration and the managed panel configuration are cross-checked against `ss -lnt`; port detection changes neither listeners nor authentication.

The exception applies only to local IPv4 INPUT from PPP, never FORWARD, fixed exits or UDP policy. IPv6 remains blocked. Rules are committed atomically with iptables-restore. Disabling or narrowing access may disconnect the current management session. Management authentication is required. A cloud NAT public IP not assigned locally requires separate address/path verification; this does not configure cloud firewalls, Nginx or SSH listeners. Permitted management traffic stays local instead of using SOCKS.


## Sign-in, languages and TCP preference

The management UI supports English, Traditional Chinese and Simplified Chinese, with language preference saved in the server configuration and cached in the browser. The saved language survives service restarts and clearing browser storage. The header contains language, help and sign-out controls; sign-out is at the far right. Form sign-in validates the existing administrator credentials on the backend; passwords are not stored in browser storage. Session cookies use HttpOnly and SameSite=Strict, plus Secure over HTTPS. Sessions expire after 12 hours or 30 idle minutes; sign-out, account changes and service restart invalidate them. Repeated login failures are temporarily throttled. Scripts use the same JSON sign-in endpoint and session cookie; upgrade verification signs in locally and discards the session afterward.

Management exceptions offer a list of local IPv4 addresses. The current panel IP is suggested only when confirmed local by the server; a sole local address is filled automatically, while multiple addresses require selection. Public NAT addresses need separate verification. Exceptions are never enabled automatically.

Google HTTPS (type 65) DNS records are forwarded unchanged through the fixed node using DoH/TCP; no synthetic empty-answer policy is applied. UDP rejection remains enabled as configured. Google diagnostics separately test the running mapping DNS TCP listener for A, AAAA and HTTPS records, then probe TCP/TLS using the mapping-resolved IPv4 addresses through the same node. An upstream empty HTTPS answer is valid. These server-side checks do not prove phone, PPP forwarding, client QUIC fallback or account access; use the mapping forwarding test and a real client to verify those paths. Stopped, unavailable or cancelled mapping checks are never reported as healthy.

## Web update failures

Web updates run in an independent systemd unit. Without a login environment, Go receives disposable build-cache and workspace directories. Failure status records the phase, exit code and a safe category (cache, toolchain, disk or download), never raw command output. Compilation failures normally precede service shutdown and installation; check final job and service status.

Configuration backups can be deleted individually or cleaned up while keeping the newest listed backup. Cleanup removes only selected managed configuration backup files; it does not touch active configuration or system upgrade backups. Confirm the selection before deletion. Monitoring tables support horizontal scrolling; mobile screens default to cards and can switch to table view. Update progress refreshes every second while a task is running, including while its disclosure is closed; idle visible panels refresh every ten seconds. Installed build version, revision, local modification state and embedded build notes are shown separately from the latest release notes. Source builds remain identified as source builds and are never relabelled as a stable release.

## Lightweight checks and optional observation

Local liveness inspection is read-only. Confirmed missing routing, firewall, MSS and configured bandwidth components are repaired in place without flushing route tables or restarting healthy processes. Permission errors, lock contention, timeouts and invalid snapshots do not authorize repair. Failed inspections back off for 5–60 seconds. Healthy upstream checks run every 45–55 seconds, with fast initial and failure confirmation. Fixed exits, DoH/TCP, active UDP rejection and IPv4/IPv6 leak guards remain enabled.

Mappings rejecting UDP skip automatic UDP probes; manual protocol tests still measure node capability. Each mapping has a private TCP DNS pool limited to four concurrent connections, with one retry for an expired idle connection. DoH supports HTTP/2 reuse. Broad protocol sniffing is removed; port 53 interception and HTTPS DNS record passthrough remain. Process logs use warning level and Debug retains bounded temporary in-memory output.

Connection observation defaults off. Enable it in advanced options while the mapping is stopped to use connection inspection and individual connection closure. Existing rate and connection limits are preserved; fixed-address PPP accounts allow one session. Google diagnostics test one selectable mapping per selected node, release lifecycle locks before network I/O and discard results after runtime replacement. Collapsed monitoring and hidden pages pause ordinary polling; active software upgrades continue polling.

## Installation checks and recovery

Use a dedicated Ubuntu/Debian host with systemd. Before application deployment, the installer verifies TUN/PPP/L2TP, firewall access and free TCP 8080/UDP 1701. Ubuntu may need the module package matching its running kernel. Existing PPP accounts, sessions and active tunnel services are refused. Go must be at least 1.22; a missing toolchain is fetched from official stable metadata with SHA-256 verification. Existing sing-box must be exactly the tested 1.11.15; downloaded amd64/arm64 archives are checksum verified, and the generated runtime schema is checked before deployment.

First-install configuration changes use a private journal in `/var/lib/s2l-install`. Failed deployment attempts restore managed files, services and network settings; dependencies remain installed. Re-running after an interruption recovers the unfinished transaction first. If ownership changed or PPP processes remain, recovery stops and retains protection for manual inspection. A completed installation requires xl2tpd, S2L and HTTPS setup plus local authenticated API verification. Existing successful installations still use the updater.

Mapping subnets must be canonical RFC1918 IPv4 networks with at least two peer addresses, excluding 172.19.0.0/16. Host network overlaps, occupied interfaces, routing tables and rule priorities are checked before startup; cleanup removes exact mapping routes instead of flushing tables. One account assigns one peer address and permits one PPP session even if an old max_sessions value was zero or greater than one. A gateway can still share that one session among many LAN devices.

CI boots fresh Ubuntu 24.04 and Debian 12 amd64 QEMU guests, exercises incompatible dependency rejection, failed-install rollback, hard-interruption recovery, real systemd services, real PPP and fault injection, then reboots the guest and repeats dialing. ARM64 receives a cross-build check; this does not establish full ARM64 or every distribution/kernel runtime compatibility.

The fresh-system fixture also tests two gateways with independent accounts and mappings sharing one SOCKS node: concurrent forwarding, duplicate-account rejection, fail-closed upstream outages, and repair or stop of one mapping without disrupting the other. These checks repeat after a full OS reboot.

Installation preflight rejects Ubuntu 6.8.0-146-generic and 6.8.0-147-generic because of the known PPPoL2TP regression (Ubuntu bug 2169477). See kernel preparation above for fresh hosts and the manual procedure for production. The full-system fixture exercises preparation, exit 20/reuse and deliberate reboot when the image needs it; unaffected images continue directly. Real dialing runs on the kernel actually booted.

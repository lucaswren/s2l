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
| DNS | A validated DNS response from `https://1.1.1.1/dns-query` through SOCKS5; if that fails, a TCP DNS probe to `8.8.8.8:53` through SOCKS5 | Working primary DoH is healthy. If only TCP DNS succeeds, the UI reports backup-only availability; this does **not** switch the active sing-box resolver |

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
- Mapping fails: inspect the mapping error, S2L journal and `/run/s2l/s2l-TUN_NAME.log`; check TUN support, sing-box version and upstream reachability.
- Client cannot dial: check credentials, enabled tunnel mode, ports, NAT, xl2tpd and, when enabled, `s2l-ipsec` logs.
- Dial succeeds but traffic fails: check upstream, assigned subnet conflicts, policy routes, firewall and client DNS/route selection.
- SSH access changes fail: retain the current connection and use a provider console or approved recovery route; inspect backup metadata before restoring.

Sanitize logs before sharing. Do not flush all host firewall rules to diagnose a mapping problem. There is no documented automated full uninstall; removal must account for existing shared services and restore saved system configuration rather than blindly deleting PPP, Nginx or firewall files.

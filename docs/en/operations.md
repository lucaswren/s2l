# Operations guide

[Home](../../README.md) · [繁體中文](../zh-TW/operations.md) · [简体中文](../zh-CN/operations.md)

Read the [legal notes](legal.md) and README security limits first. Keep security updates enabled; wait for package-manager locks instead of routinely disabling unattended updates. Use maintenance windows for changes that interrupt connections.

## Access and client setup

The installation prints the HTTPS URL, username and initial password. Restrict the management TCP port to trusted source IPs in both host firewall and cloud security groups. Keep backend TCP 8080 private. Add an authorized SOCKS5 node and an L2TP mapping, then start it. A `running` status is not an end-to-end connectivity or security test: verify the intended egress, DNS, IPv6 handling and failure behavior from an actual client.

| Mode | Network access | Protection |
|---|---|---|
| Management | Selected HTTPS TCP port | TLS for web/API only |
| Plain L2TP (default) | UDP 1701 from authorized clients | No tunnel encryption |
| L2TP/IPsec | UDP 500/4500 and ESP, protocol 50 | IKEv1/shared PSK; test NAT and client compatibility |

Prefer encrypted client transport where compatible with your security policy. In System Settings, enable IPsec with a PSK of at least 12 characters; all mappings share it. A blank PSK retains the existing key. The server blocks plain L2TP when IPsec is enabled; disabling IPsec requires clearing the client PSK. Reconnect clients after mode/key changes. Failures in IPsec configuration/firewall changes trigger rollback; the helper refuses detected conflicts with existing IPsec services. Backup metadata: `/opt/s2l/ipsec-backup.json`.

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
| 0 | Exit menu |

Admin passwords are 12–128 characters; the installer/CLI allow a restricted character set. CLI account changes restart s2l and may interrupt tunnels; web account changes take effect without a service restart. Log in again afterwards. The CLI uses `public_url` when configured; otherwise it derives the address from the listener and detected IP, with a NAT warning on fallback.

Before changing any access port, allow the new port first. Verify the new URL or SSH connection before closing the old session. SSH changes validate `sshd` configuration and listening state, with rollback on failure, and support Ubuntu `ssh.socket`; backup metadata is `/opt/s2l/ssh-port-backup.json`. Web HTTPS port changes reload Nginx, preserve the backend and certificate, and roll back validation/reload failures.

The web root-password action requires authenticated HTTPS access. It accepts 12–128 visible ASCII characters or generates a 16-character password. It changes the Linux root password, not web credentials; it does not enable SSH password authentication or unlock the account. Retain the original SSH session and verify access separately.

## HTTPS certificates

For an existing installation, review and run from the checkout after allowing the selected HTTPS port:

```bash
sudo bash script/setup_https.sh YOUR_PUBLIC_IPV4 8443
s2l 1
```

Verify the printed SHA-256 fingerprint before trusting/importing `/opt/s2l/tls/self-signed.crt`. Do not share its private key. Self-signed certificates are not publicly trusted. No TCP 80 or public CA renewal is required by this setup. Before certificate expiry, rerun the helper; it regenerates when validity is under one day. Omitting the port on an existing HTTPS installation preserves its current port. Configuration failures restore the previous configuration.

## Backups and updates

Routing table IDs must be 1–252; Linux reserves 253–255. Assign only tables dedicated to s2l: cleanup flushes the selected table. Existing mappings using reserved IDs must be corrected before starting; do not manually flush system tables. Runtime files now live under private `/run/s2l` (0700), with 0600 logs. Install the updated service unit or let the root process create that directory. Old `/tmp/s2l-*` files from earlier versions are not automatically migrated; review and remove only verified stale project files after stopping old processes.

Installed configuration: `/opt/s2l/config.json`; installed state: `/opt/s2l/data/state.json`. Also protect TLS keys, PPP credentials, Nginx/systemd configuration and any SSH/IPsec files touched by helpers. Generated sing-box files/logs in `/run/s2l/s2l-*` may contain sensitive information. Restrict permissions and encrypt off-host backups. Do not upload them to public issues.

Before an update, record the deployed commit and versions, and take a consistent snapshot during a maintenance window with s2l stopped. Preserve configuration, state and the previous binary; also snapshot system networking/PPP/IPsec configuration. A binary-only rollback may not undo schema or system changes.

Build a reviewed revision in a separate checkout using the development guide. After verification, stop s2l, replace only the application binary with the built `bin/s2l`, and start the service. Review helper/unit changes separately before installing them. Do not use the new-install scripts as an updater: `install.sh` refuses existing configuration, and the remote script can reset its source checkout. Confirm mappings and actual client traffic after restart. If validation fails, stop the service and restore the tested snapshot/previous binary and matching state/configuration before restarting. Restore privileged files only with correct ownership and permissions.

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
- Mapping fails: inspect the mapping error, s2l journal and `/run/s2l/s2l-TUN_NAME.log`; check TUN support, sing-box version and upstream reachability.
- Client cannot dial: check credentials, enabled tunnel mode, ports, NAT, xl2tpd and, when enabled, `s2l-ipsec` logs.
- Dial succeeds but traffic fails: check upstream, assigned subnet conflicts, policy routes, firewall and client DNS/route selection.
- SSH access changes fail: retain the current connection and use a provider console or approved recovery route; inspect backup metadata before restoring.

Sanitize logs before sharing. Do not flush all host firewall rules to diagnose a mapping problem. There is no documented automated full uninstall; removal must account for existing shared services and restore saved system configuration rather than blindly deleting PPP, Nginx or firewall files.

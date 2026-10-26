# Client setup and DNS

[Home](../../README.md) · [English](../en/clients.md) · [繁體中文](../zh-Hant/clients.md) · [简体中文](../zh-Hans/clients.md)

## Before dialing

Start a mapping and record the server address, L2TP username/password and the mode in System settings. If IPsec is enabled, use L2TP/IPsec with the shared PSK; all mappings share this setting. Plain L2TP is unencrypted and is not offered by every native client. Support depends on the client OS/version. A mapping allows one PPP session; give each independently dialing device its own mapping/account. Multiple LAN devices can share a router's single session.

The server needs a reachable IPv4 address. The installer and HTTPS helper require a public IPv4; automatic public-address discovery does not establish inbound reachability. A server behind NAT needs explicit forwarding and matching firewall rules. CGNAT without inbound forwarding is unsuitable for ordinary remote dialing.

| Path | Requirements |
|---|---|
| Client → plain L2TP server | UDP 1701 |
| Client → IPsec server | UDP 500/4500; NAT-T is enforced |
| Server → fixed SOCKS5 | TCP reachability, valid credentials and working CONNECT; UDP ASSOCIATE plus a reachable relay only for permitted UDP applications |
| SOCKS5 → resolver/target | Access to the configured DoH HTTPS endpoint or TCP DNS endpoint and application destinations |

Host firewall, cloud security group, upstream NAT and client network must all permit the chosen mode. HTTPS panel access does not prove VPN reachability. When IPsec is enabled, unprotected L2TP is blocked; do not open bare UDP 1701 as a workaround. See [strongSwan NAT-T documentation](https://docs.strongswan.org/docs/latest/features/natTraversal.html).


S2L now separates IPsec clients sharing one public NAT address even when both use L2TP source port 1701. Each SA has its own XFRM mark, conntrack zone and server-internal peer port; client ports and fixed SOCKS5 mappings stay unchanged. This configuration enforces NAT-T (UDP 500/4500), including clients without NAT. Linux real-dial tests cover two simultaneous 1701 clients, rekey, redial, DNS, UDP and upstream failure. Native Windows/macOS/router clients and a server behind NAT still require deployment-specific validation; these results do not certify every client implementation.

For SOCKS5 UDP, allowing the proxy's **TCP service port is insufficient**. The S2L server must reach the UDP address and port returned by `UDP ASSOCIATE`, which can be a dynamically allocated port different from the TCP port; its return path and the proxy's own outbound UDP also need to work. Keep the association's TCP control connection alive. A successful association is not proof of UDP forwarding. The built-in check validates actual UDP DNS replies; it does not prove every UDP application or QUIC works. `reject-quic` rejects all UDP/443, not protocol-detected QUIC; TCP MSS does not change UDP datagram sizes.

When TCP/DoH succeeds and UDP ASSOCIATE is accepted but no datagram returns, compare the same node on an independently selected authorized network interface. A local VPN/TUN, policy route or upstream firewall can discard the relay traffic. Keep TCP control and UDP on the same egress interface; binding only the TCP connection is insufficient. A positive result on another path rules out blanket lack of UDP support, but does not identify which device on the failing path drops packets. Never make client traffic fall back to direct egress to work around a failed relay.

## DNS offered to the client versus actual resolvers

PPP offers **1.1.1.1 and 8.8.8.8** during IPCP negotiation. Accept automatic VPN DNS. Upgrade existing servers using the [update procedure](operations.md), then reconnect clients to renegotiate DNS. Existing sessions do not receive changed PPP options. Do not hand-edit the generated PPP options file.

These addresses identify DNS requests inside the tunnel. All TCP/UDP port 53 traffic reaching a mapping's TUN is intercepted; actual DNS exchanges use that mapping's fixed SOCKS5 node. Custom resolvers do not change the addresses offered to clients. No public DNS listener or PPP-to-host firewall exception is required. If an offered address is assigned locally on the S2L host, use a different, nonlocal DNS destination routed into the tunnel: local INPUT is blocked before TUN interception.

Stop the mapping, open **Edit → Advanced options**, configure the following, save and start it:

| UI / JSON field | Default | Accepted values |
|---|---|---|
| DNS mode / `dns_mode` | `auto` | `auto`, `doh`, `tcp` |
| DoH URL / `dns_doh_url` | `https://1.1.1.1/dns-query` | HTTPS URL; optional port/path; no credentials, query, fragment or whitespace |
| TCP DNS address / `dns_tcp_address` | `8.8.8.8:53` | `host:port` or `[IPv6]:port`, port 1–65535; no scoped IPv6 |

Empty fields restore defaults; omitted fields in older state files keep the same defaults. Resolver hostnames are sent to SOCKS5 for remote resolution. The SOCKS5 server's own hostname may still need the S2L host's resolver; an upstream IP address avoids that bootstrap lookup. IPv6 resolver endpoints require the SOCKS5 server to support them; this does not enable IPv6 PPP forwarding.

For example, a mapping JSON can contain:

```json
{
  "dns_mode": "tcp",
  "dns_doh_url": "",
  "dns_tcp_address": "10.20.0.53:53"
}
```

This is a fragment of a full mapping, not a standalone import. The private DNS address must be reachable **from the SOCKS5 exit**. Use a resolver that answers both required public and private names; no per-domain split-DNS rules are implemented. In auto mode configure both endpoints with the intended DNS view, or select a forced mode to avoid falling back to a public default for private names.

Auto mode tries TCP after a failed DoH query and prefers TCP after repeated primary failures; recovery checks can restore DoH. It never switches SOCKS5 nodes or falls back to a direct server connection. Forced modes use only their chosen transport. Working DNS does not require SOCKS5 UDP support. Valid NXDOMAIN does not trigger fallback. Changing endpoints restarts the mapping resolver and its sing-box cache; clear client/application caches if needed. Text imports preserve existing custom DNS; JSON imports use submitted values. Backups retain both fields. Automatic health probes use the configured endpoints; manual node probes use defaults and do not overwrite custom mapping DNS health.

## Client settings

| Client | Setup and routing |
|---|---|
| Windows | Choose L2TP/IPsec with PSK when the server uses IPsec. Enter the mapping credentials and use an authentication method compatible with the server's MS-CHAPv2 requirement. Accept automatic DNS on the VPN adapter. For full IPv4 tunneling, enable the VPN default route / remote default gateway; verify the effective routes. |
| macOS / iOS | If the installed version offers L2TP/IPsec, match the server, account and shared secret. Enable sending all traffic through the VPN where available and check VPN DNS. A client with no plain-L2TP option requires IPsec on S2L. |
| Linux | Use an L2TP client, and an IPsec client when required. With pppd, enable `usepeerdns`. Integrate received DNS with NetworkManager, systemd-resolved or the distribution's PPP hooks; receiving DNS alone does not guarantee the system resolver uses it. Configure VPN routes separately. |
| Router / gateway | One account per dialed session. Route the intended LAN IPv4 traffic into L2TP and source-NAT it to the assigned PPP address. Make router-generated DNS queries use that path too; a LAN policy route may not affect the router's own queries. Keep a route to the VPN server through the original WAN. |
| Android / other devices | Check whether that version/client supports L2TP/IPsec with PSK. Do not assume every current system has a built-in compatible client. |

pppd exposes negotiated addresses to the IP-up hook as `DNS1`/`DNS2` and can write `/etc/ppp/resolv.conf`; that file is distinct from the system resolver configuration. See the [pppd manual](https://github.com/ppp-project/ppp/blob/master/pppd/pppd.8). Avoid blindly overwriting `/etc/resolv.conf` on a managed system.

If automatic DNS is ignored, enter 1.1.1.1 and 8.8.8.8 manually **and route both into L2TP**. Check LAN DNS, browser secure DNS, application DoH and split routing separately. DoH on HTTPS does not use S2L's port 53 interception; it follows the client's application routing.

S2L blocks IPv6 arriving through PPP, but cannot block IPv6 through a client's other adapters. Enforce the required IPv6 and disconnect-fallback policy on the client/router. An inactive VPN must not silently restore WAN access if the intended policy is to fail closed.

## Alternative VPN protocols

S2L currently terminates L2TP, with optional L2TP/IPsec. This October 2026 comparison describes candidates, not implemented S2L modes. Confirm firmware and license; protocol recognition does not imply a VPN client.

| Protocol | Panabit standard/free | iKuai free | RouterOS 7 |
|---|---|---|---|
| L2TP | Current deployment verified | Client available | Supported |
| Native IPsec | Module documented; verify IKE version/selectors | Verify authentication and routing | Supported |
| IKEv2/IPsec | Client support unconfirmed | Official 3.7 history labels it enterprise | Supported |
| OpenVPN | Native client unconfirmed | Client documented | Supported; v7 adds UDP |
| WireGuard | Native client unconfirmed | Official 3.7 history labels it enterprise | Supported |
| iWAN | Vendor protocol | Native interoperability unconfirmed | Native interoperability unconfirmed |

Sources: [Panabit standard release](https://panabit.com/Article?article_id=148), [iWAN](https://www.panabit.com/Article?article_id=134), [iKuai license history](https://www.ikuai8.com/index.php?id=331&option=com_content&view=article), [iKuai OpenVPN](https://www.ikuai8.com/support/ymgn/lyym/wlsz/vpn/openvpn.html), [RouterOS specifications](https://manual.mikrotik.com/docs/getting-started/software-specifications/), [RouterOS v7 changes](https://manual.mikrotik.com/docs/getting-started/upgrading-to-v7/). Newer free iKuai builds unlocking enterprise features have not been verified.

For all three free router families, retain L2TP while evaluating native IPsec interoperability; IPsec requires matching identities, proposals and routes. For iKuai/RouterOS, OpenVPN is another candidate. WireGuard is simple where licensed/supported, but is not a verified common free option. Give each concurrent device its own key and tunnel address; multiple devices can share a SOCKS5 exit without sharing a VPN identity. See [WireGuard peer configuration](https://www.wireguard.com/quickstart/).

Adding a protocol to S2L requires ingress identity, routing, DNS, firewall and recovery work: `ppp+` guards do not automatically protect WireGuard/OpenVPN interfaces. Changing VPN protocols does not fix upstream SOCKS5 failures, shared rate limits or router policy conflicts. Evaluate on an isolated pilot while retaining the current iWAN/L2TP path.

## Acceptance checks after connection

1. Check assigned PPP IPv4, both DNS addresses and effective routes. Windows: `ipconfig /all`, `route print -4`; Linux: `ip -4 addr`, `ip -4 route get 1.1.1.1`, and `resolvectl status` when available; macOS: `scutil --dns`, `netstat -rn -f inet`. The VPN server itself must remain reachable via the original network.
2. Test both offered DNS addresses explicitly, for example `nslookup example.com 1.1.1.1` and `nslookup example.com 8.8.8.8`, then normal system/application resolution. For custom private DNS, also query a name unique to that resolver's view. Check mapping DNS counters; a direct client-to-public-resolver response alone does not prove the VPN path.
3. Open HTTPS from the actual client and confirm the expected SOCKS5 egress. A rotating provider can return different IPs without switching configured nodes. Panel speed/protocol tests are server-side evidence, not a replacement for client tests.
4. Default policy actively rejects non-DNS UDP. Applications may retry over TCP; UDP-only applications need an allowing policy and a capable SOCKS5 relay. Test the actual application and check its MTU/MSS sensitivity.
5. In a maintenance window, interrupt the selected upstream and verify application/DNS failure without server-public-IP fallback. Restore it and test recovery. Separately disconnect the VPN to verify the client's own fallback and IPv6 policy. Test simultaneous devices behind a gateway and reject a duplicate dial using the same account.

If dialing works but domains fail, first check VPN DNS adoption and routes, then the custom resolver's reachability from SOCKS5 and its responses. Healthy default node DNS does not prove that a custom private resolver works. The [operations guide](operations.md) covers health, logs, recovery and leak-guard boundaries.

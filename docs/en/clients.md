# Client setup and DNS

[Home](../../README.md) · [English](../en/clients.md) · [繁體中文](../zh-Hant/clients.md) · [简体中文](../zh-Hans/clients.md)

## Before dialing

Start a mapping and record the server address, L2TP username/password and the mode in System settings. If IPsec is enabled, use L2TP/IPsec with the shared PSK; all mappings share this setting. Plain L2TP is unencrypted and is not offered by every native client. Support depends on the client OS/version. A mapping allows one PPP session; give each independently dialing device its own mapping/account. Multiple LAN devices can share a router's single session.

The server needs a reachable IPv4 address. The installer and HTTPS helper require a public IPv4; automatic public-address discovery does not establish inbound reachability. A server behind NAT needs explicit forwarding and matching firewall rules. CGNAT without inbound forwarding is unsuitable for ordinary remote dialing.

| Path | Requirements |
|---|---|
| Client → plain L2TP server | UDP 1701 |
| Client → IPsec server | UDP 500/4500; NAT-T is enforced |
| Server → fixed SOCKS5 | TCP reachability, valid credentials and working CONNECT; manual UDP capability tests also need UDP ASSOCIATE and a reachable relay |
| SOCKS5 → resolver/target | Access to the configured DoH HTTPS endpoint or TCP DNS endpoint and application destinations |

Host firewall, cloud security group, upstream NAT and client network must all permit the chosen mode. HTTPS panel access does not prove VPN reachability. When IPsec is enabled, unprotected L2TP is blocked; do not open bare UDP 1701 as a workaround. See [strongSwan NAT-T documentation](https://docs.strongswan.org/docs/latest/features/natTraversal.html).

S2L now separates IPsec clients sharing one public NAT address even when both use L2TP source port 1701. Each SA has its own XFRM mark, conntrack zone and server-internal peer port; client ports and fixed SOCKS5 mappings stay unchanged. This configuration enforces NAT-T (UDP 500/4500), including clients without NAT. Linux real-dial tests cover two simultaneous 1701 clients, rekey, redial, DNS, UDP and upstream failure. Native Windows/macOS/router clients and a server behind NAT still require deployment-specific validation; these results do not certify every client implementation.

The manual node UDP check measures upstream capability, not client forwarding: managed mappings reject non-DNS UDP. The server must reach the UDP relay returned by `UDP ASSOCIATE`, which may use a different port from the SOCKS5 TCP service. Keep its TCP control connection alive and both directions reachable. A valid UDP DNS reply does not prove every UDP application or QUIC works.

S2L terminates L2TP, with optional L2TP/IPsec. WireGuard, OpenVPN and native IKEv2 are not implemented. Check the installed client or router firmware for L2TP compatibility before deployment.

## DNS offered to the client versus actual resolvers

PPP offers **8.8.8.8 and 1.1.1.1** during IPCP negotiation. Accept automatic VPN DNS. Upgrade existing servers using the [update procedure](operations.md), then reconnect clients to renegotiate DNS. Existing sessions do not receive changed PPP options. Do not hand-edit the generated PPP options file.

These addresses identify DNS requests inside the tunnel. All TCP/UDP port 53 traffic reaching a mapping's TUN is intercepted; actual DNS exchanges use that mapping's fixed SOCKS5 node. No public DNS listener or PPP-to-host firewall exception is required. If an offered address is assigned locally on the S2L host, use a different, nonlocal DNS destination routed into the tunnel: local INPUT is blocked before TUN interception.

DNS is managed automatically. The mapping prefers TCP DNS at `8.8.8.8:53` and uses `https://1.1.1.1/dns-query` as a fallback through its fixed SOCKS5 exit. A mapping that requires a private resolver needs a separately reviewed deployment; the managed defaults resolve public names.

A failed transport starts the alternate immediately. If no valid response arrives within 300 ms, the alternate runs concurrently; the first valid answer wins and cancels the other request. After two confirmed TCP DNS failures with DoH available, the resolver temporarily prefers DoH. Two successful TCP DNS checks restore the preference. A live TCP DNS answer counts toward recovery when DoH fails or stalls. Valid NXDOMAIN does not trigger fallback. Both methods always use the same SOCKS5 node; if that node is unavailable, alternate DNS cannot restore its connectivity. No direct server DNS or exit switching is used. Working DNS does not require SOCKS5 UDP support. sing-box retains normal DNS caching.

## Client settings

| Client | Setup and routing |
|---|---|
| Windows | Choose L2TP/IPsec with PSK when the server uses IPsec. Enter the mapping credentials and use an authentication method compatible with the server's MS-CHAPv2 requirement. Accept automatic DNS on the VPN adapter. For full IPv4 tunneling, enable the VPN default route / remote default gateway; verify the effective routes. |
| macOS / iOS | If the installed version offers L2TP/IPsec, match the server, account and shared secret. Enable sending all traffic through the VPN where available and check VPN DNS. A client with no plain-L2TP option requires IPsec on S2L. |
| Linux | Use an L2TP client, and an IPsec client when required. With pppd, enable `usepeerdns`. Integrate received DNS with NetworkManager, systemd-resolved or the distribution's PPP hooks; receiving DNS alone does not guarantee the system resolver uses it. Configure VPN routes separately. |
| Router / gateway | One account per dialed session. Route the intended LAN IPv4 traffic into L2TP and source-NAT it to the assigned PPP address. Make router-generated DNS queries use that path too; a LAN policy route may not affect the router's own queries. Keep a route to the VPN server through the original WAN. |
| Android / other devices | Check whether that version/client supports L2TP/IPsec with PSK. Do not assume every current system has a built-in compatible client. |

pppd exposes negotiated addresses to the IP-up hook as `DNS1`/`DNS2` and can write `/etc/ppp/resolv.conf`; that file is distinct from the system resolver configuration. See the [pppd manual](https://github.com/ppp-project/ppp/blob/master/pppd/pppd.8). Avoid blindly overwriting `/etc/resolv.conf` on a managed system.

If automatic DNS is ignored, enter 8.8.8.8 and 1.1.1.1 manually **and route both into L2TP**. Check LAN DNS, browser secure DNS, application DoH and split routing separately. DoH on HTTPS does not use S2L's port 53 interception; it follows the client's application routing.

S2L blocks IPv6 arriving through PPP, but cannot block IPv6 through a client's other adapters. Enforce the required IPv6 and disconnect-fallback policy on the client/router. An inactive VPN must not silently restore WAN access if the intended policy is to fail closed.

## Acceptance checks after connection

1. Check assigned PPP IPv4, both DNS addresses and effective routes. Windows: `ipconfig /all`, `route print -4`; Linux: `ip -4 addr`, `ip -4 route get 1.1.1.1`, and `resolvectl status` when available; macOS: `scutil --dns`, `netstat -rn -f inet`. The VPN server itself must remain reachable via the original network.
2. Test both offered DNS addresses explicitly, for example `nslookup example.com 1.1.1.1` and `nslookup example.com 8.8.8.8`, then normal system/application resolution. Check mapping DNS counters; a direct client-to-public-resolver response alone does not prove the VPN path.
3. Open HTTPS from the actual client and confirm the expected SOCKS5 egress. A rotating provider can return different IPs without switching configured nodes. Panel speed/protocol tests are server-side evidence, not a replacement for client tests.
4. Managed mappings actively reject non-DNS UDP and do not offer a per-mapping UDP switch. Applications need TCP fallback; UDP-only applications require a separately reviewed deployment. Test the actual application and its MTU/MSS sensitivity.
5. In a maintenance window, interrupt the selected upstream and verify application/DNS failure without server-public-IP fallback. Restore it and test recovery. Separately disconnect the VPN to verify the client's own fallback and IPv6 policy. Test simultaneous devices behind a gateway and reject a duplicate dial using the same account.

If dialing works but domains fail, first check VPN DNS adoption and routes, then reachability and replies from the managed DoH/TCP resolvers through SOCKS5. The [operations guide](operations.md) covers health, logs, recovery and leak-guard boundaries.

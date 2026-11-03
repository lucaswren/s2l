# Third-party components

S2L's original code and documentation use Apache-2.0. That license does not replace third-party terms. This inventory is a starting point, not a certification of compliance for every distribution. Verify the exact versions and artifacts you ship, including transitive dependencies and operating-system packages.

| Component | Relationship | License reference |
|---|---|---|
| sing-box | Separately downloaded executable; installer default is v1.11.15 | [Versioned license](https://github.com/SagerNet/sing-box/blob/v1.11.15/LICENSE): GPL-3.0-or-later, plus naming/association notice |
| Vue / Element Plus | Bundled frontend runtime | MIT; exact installed license texts in `web/THIRD_PARTY_LICENSES.txt` |
| Frontend transitive packages | Runtime or build tooling, as recorded in `web/package-lock.json` | Individual package licenses; collected texts in `web/THIRD_PARTY_LICENSES.txt` |
| Vite / Vue Vite plugin | Frontend build tools | MIT; package notices in the collected texts |
| Nginx | Separate system package for HTTPS | [Official license](https://nginx.org/LICENSE) |
| strongSwan | Separate system package for optional IPsec | [Official project license information](https://docs.strongswan.org/docs/latest/devs/contributions.html) |
| xl2tpd | Separate system L2TP daemon | [Upstream repository](https://github.com/xelerance/xl2tpd); check the installed package's copyright file |
| PPP | Separate system package | [Upstream licensing overview](https://github.com/ppp-project/ppp/blob/master/COPYING); individual source files and plugins have their own terms |
| Go standard library | Included by Go compilation | [Go license](https://go.dev/LICENSE) |

Ubuntu/Debian package copyright files are normally available under `/usr/share/doc/PACKAGE/copyright`. Installation also uses Python, OpenSSL, iptables, iproute2 and other system tools, whose installed distribution terms must be reviewed separately.

The Go module currently has no external module dependencies. The frontend lockfile records build and runtime dependency versions; not every installed build package is included in the shipped browser assets. The collected license file deliberately includes installed dependency notices as a conservative inventory and must be regenerated when the lockfile changes.

The installer downloading an upstream binary does not license that binary under Apache-2.0. If you redistribute sing-box or a bundled image, meet its own license/notice and corresponding-source obligations as applicable. If you copy or link third-party code into S2L, reassess compatibility; separate-process invocation alone is not a universal legal conclusion about a combined product.

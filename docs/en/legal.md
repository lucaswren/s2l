# Legal and compliance notes

[Home](../../README.md) · [繁體中文](../zh-Hant/legal.md) · [简体中文](../zh-Hans/legal.md)

Reference check: 2026-10-11. These notes identify deployment risks, not a legal opinion or an exhaustive statement of current law. Applicability depends on location, network design, users, business model and data. Obtain qualified local advice and any required approvals before proceeding. A disclaimer, free distribution or an educational purpose does not remove legal obligations or liability.

## Mainland China

The [international networking provisions](https://hbca.miit.gov.cn/xxgk/zcwj/flfg/art/2020/art_b118b9e4e97d4a8e9e6af358c78ac4dc.html), particularly Articles 6, 10 and 14, regulate channels and user access and provide administrative consequences for violations. The text also addresses links with Hong Kong, Macao and Taiwan in Article 16. Their application to a specific technical arrangement requires individual assessment.

The [2017 MIIT notice](https://www.miit.gov.cn/zwgk/zcwj/wjfb/tz/art/2017/art_a940645e940946e1a62cd6c90a4e994e.html) addresses unauthorized cross-border business over leased lines/VPNs. The [official clarification](https://www.cac.gov.cn/2017-01/26/c_1120381529.htm) explains that focus and enterprise office connectivity through qualified operators. It is not blanket permission for private proxy deployments. The notice's historical campaign dates do not establish a current exemption.

Do not infer that personal use, a server abroad, no fees, or SOCKS5/L2TP terminology makes a deployment lawful. Before mainland or cross-border deployment, confirm the permitted connectivity arrangement with a qualified operator and counsel. Do not operate or resell access services without required permissions, or use this project for unlawful circumvention of network controls. Until the legal basis is clear, use an isolated authorized lab without cross-border service provision.

## Other obligations and third parties

Assess applicable telecommunications licensing, privacy, personal-information handling, cross-border data transfer, cybersecurity, confidentiality and provider contracts. Determine obligations for your actual role and data rather than assuming a universal filing or logging requirement. Do not route sensitive or regulated information until the necessary assessment and controls are complete. Mainland conclusions should not be applied automatically to other jurisdictions.

The code uses external services: generated sing-box configurations reference Cloudflare DoH (`1.1.1.1`) and Google TCP DNS (`8.8.8.8`); speed tests contact Cloudflare through the selected upstream. Public-IP discovery and dependency downloads also contact third parties. Review destinations, terms and data exposure in your deployment; this is not an offline-only tool. Neither tunneling nor HTTPS guarantees confidentiality from upstream operators or lawful data transfer.

## Source and licenses

Original S2L code and documentation are licensed under [Apache-2.0](../../LICENSE). Preserve applicable copyright/license notices and comply with its redistribution terms. Contributions must be submitted with sufficient rights under the project license. Review the licenses and notices of sing-box, xl2tpd, PPP, strongSwan, Nginx, frontend packages and other dependencies separately; the project license does not override them. See [third-party notices](../../THIRD_PARTY_NOTICES.md).

When revising these notes, verify official sources, preserve the distinction between networking and business regulation, and update all three language editions. Do not make an unsupported claim that every VPN is prohibited or that a particular deployment is approved.

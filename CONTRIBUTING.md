# Contributing to S2L

Bug reports, documentation corrections and narrowly scoped fixes are welcome. Use [Discussions](https://github.com/lucaswren/s2l/discussions) for setup questions and [Issues](https://github.com/lucaswren/s2l/issues/new/choose) for reproducible defects or specific feature requests. 中文反馈同样欢迎。

Include the S2L version, distribution, kernel, client type, plain L2TP or IPsec mode, symptoms and reproduction steps. For outages, record the time and timezone, affected mappings, whether they share a SOCKS5 node, and which of local forwarding, upstream and DNS checks failed. Capture evidence before restarting where possible. Remove passwords, PSKs, private configuration and customer identifiers before sharing. Report vulnerabilities through [SECURITY.md](SECURITY.md).

## Development checks

Use a supported Go toolchain compatible with `go.mod`, Node.js 22 as used in CI, and Python 3. Keep changes focused and describe the trigger, resulting behavior and relevant validation in a pull request.

```bash
go test ./...
go vet ./...
python3 -m unittest discover -s script/tests
npm --prefix web ci
npm --prefix web test
npm --prefix web run build
python3 script/check_docs.py
```

Commit `web/dist/` with frontend changes. Keep source files in LF format so Windows and Linux build the same embedded assets. Linux CI also runs race detection, real PPP/IPsec faults, payload-free capture, upgrades and fresh Ubuntu/Debian installations. Tests gated for network namespaces or disposable virtual machines must never be enabled on a production server; see the [development guide](docs/en/development.md).

Documentation screenshots come from isolated example fixtures, not live customer systems. The [screenshot workflow](https://github.com/lucaswren/s2l/actions/workflows/showcase.yml) produces three views in English, Traditional Chinese and Simplified Chinese. Review all nine PNGs: default English images belong in `docs/assets/`, Traditional Chinese in `docs/assets/zh-Hant/`, and Simplified Chinese in `docs/assets/zh-Hans/`. Each README must link to its matching interface language. `script/check_docs.py` checks these references along with local file links and heading fragments.

#!/usr/bin/env bash
# Upgrade an existing default /opt/s2l deployment without resetting its source.
set -euo pipefail
umask 077
[[ "$(id -u)" -eq 0 ]] || { echo "请使用 root：curl ... | sudo bash" >&2; exit 1; }
[[ -f /opt/s2l/config.json && -x /opt/s2l/s2l ]] || { echo "[ERROR] 未找到现有 S2L 安装，请使用新安装脚本。" >&2; exit 1; }
for command in curl tar; do command -v "$command" >/dev/null || { echo "[ERROR] 缺少 $command" >&2; exit 1; }; done
S2L_REF="${S2L_REF:-main}"
[[ "$S2L_REF" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ ]] || { echo "[ERROR] 无效的 S2L_REF" >&2; exit 1; }
work="$(mktemp -d /tmp/s2l-download.XXXXXXXX)"
cleanup() { [[ "$work" == /tmp/s2l-download.* ]] && rm -rf -- "$work"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$work/source"
echo "[INFO] 下载 S2L ($S2L_REF)，现有服务继续运行..."
curl -fSL --retry 2 --connect-timeout 15 --max-time 180   "https://codeload.github.com/lucaswren/s2l/tar.gz/$S2L_REF" -o "$work/source.tar.gz"
tar -xzf "$work/source.tar.gz" --strip-components=1 -C "$work/source"
[[ -f "$work/source/script/update.sh" ]] || { echo "[ERROR] 所选版本不支持一键升级。" >&2; exit 1; }
export S2L_REF
bash "$work/source/script/update.sh"

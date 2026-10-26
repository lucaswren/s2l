#!/usr/bin/env bash
# Run from any reviewed source checkout. Only the default managed layout is supported.
set -euo pipefail
umask 077
[[ "$(id -u)" -eq 0 ]] || { echo "[ERROR] 请使用 sudo bash script/update.sh" >&2; exit 1; }
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="/usr/local/go/bin:$PATH"
for command in go python3 systemctl flock iptables iptables-restore ip6tables iptables-save ip6tables-save install cp; do
  command -v "$command" >/dev/null || { echo "[ERROR] 缺少 $command；现有服务未修改。" >&2; exit 1; }
done
exec 9>/run/s2l-update.lock
flock -n 9 || { echo "[ERROR] 另一个 S2L 升级正在运行。" >&2; exit 1; }
python3 "$ROOT_DIR/script/verify_update.py" preflight /opt/s2l/config.json
[[ -x /opt/s2l/s2l && -f "$ROOT_DIR/web/dist/index.html" ]] || { echo "[ERROR] 缺少已安装程序或内嵌前端。" >&2; exit 1; }
for file in deploy/systemd/{s2l,s2l-guard,s2l-ipsec}.service deploy/systemd/xl2tpd-s2l.conf script/{s2l.sh,remote_update.sh,manage_config.py,manage_ssh.py,manage_ipsec.py,ipsec_nat.py,manage_time.py,manage_kernel.py,manage_update.py,verify_update.py}; do
  [[ -f "$ROOT_DIR/$file" ]] || { echo "[ERROR] 源码不完整：$file" >&2; exit 1; }
done
report_web_update() {
  if [[ -n "${S2L_WEB_UPDATE_ID:-}" && -f /opt/s2l/manage_update.py ]]; then
    python3 /opt/s2l/manage_update.py phase "$S2L_WEB_UPDATE_ID" "$1" >/dev/null 2>&1 || true
  fi
}
# Older managed installations did not include conntrack. Prepare this dependency
# before building, stopping services or changing application files. Do not upgrade
# unrelated packages or let needrestart restart live services during preparation.
if ! command -v conntrack >/dev/null; then
  dependency_error() {
    echo "[ERROR] conntrack dependency installation failed; 现有 S2L 服务未停止。请检查 apt 软件源/锁，安装 conntrack 后重试。" >&2
    exit 1
  }
  command -v apt-get >/dev/null && command -v timeout >/dev/null || dependency_error
  echo "[INFO] 补装 conntrack，现有服务继续运行..."
  apt_dependency() {
    DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l timeout --kill-after=30s 240s apt-get \
      -o DPkg::Lock::Timeout=120 -o Acquire::Retries=2 \
      -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20 "$@"
  }
  apt_dependency update -qq || dependency_error
  apt_dependency install -y --no-install-recommends --no-upgrade conntrack || dependency_error
  command -v conntrack >/dev/null || dependency_error
fi
work="$(mktemp -d /tmp/s2l-build.XXXXXXXX)"
backup=""
changed=0
complete=0
snapshot=0
was_s2l=0
was_l2tp=0
was_ipsec=0

finish() {
  local result=$?
  trap - EXIT INT TERM
  if (( changed && !complete )); then
    report_web_update rolling_back
    echo "[ERROR] 升级未完成，停止拨号服务并尝试恢复备份..." >&2
    systemctl stop xl2tpd s2l s2l-ipsec || true
    if (( snapshot )); then
      cp -a "$backup/app/." /opt/s2l/ || echo "[ERROR] 程序/配置恢复失败，请使用备份手动恢复。" >&2
      while IFS= read -r file; do
        cp -a "$backup/system$file" "$file" || echo "[ERROR] 系统文件恢复失败：$file" >&2
      done < "$backup/system-paths.txt"
    fi
    # Keep the new guard unit/drop-in even when reverting an older binary.
    # Unsupported -guard-only then fails closed on boot instead of allowing PPP.
    install -m 644 "$ROOT_DIR/deploy/systemd/s2l-guard.service" /etc/systemd/system/s2l-guard.service || true
    mkdir -p /etc/systemd/system/xl2tpd.service.d
    install -m 644 "$ROOT_DIR/deploy/systemd/xl2tpd-s2l.conf" /etc/systemd/system/xl2tpd.service.d/s2l.conf || true
    systemctl daemon-reload || true
    if [[ -x "$work/s2l" ]]; then "$work/s2l" -guard-only || echo "[ERROR] 阻断规则安装失败；请勿启动拨号服务。" >&2; fi
    echo "[ERROR] 拨号服务保持停止；备份：${backup:-未创建}。请检查 journalctl -u s2l -u s2l-guard，再重试升级。" >&2
    (( result != 0 )) || result=1
  fi
  [[ "$work" == /tmp/s2l-build.* ]] && rm -rf -- "$work"
  exit "$result"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# A transient system service can have no login HOME/XDG environment. Keep
# Go's required writable cache and workspace inside this private build dir.
# Preserve normal SSH/custom caches when their environment is available.
if [[ -z "${GOCACHE:-}" && -z "${HOME:-}" && -z "${XDG_CACHE_HOME:-}" ]]; then
  export GOCACHE="$work/go-build"
fi
if [[ -z "${GOPATH:-}" && -z "${HOME:-}" ]]; then
  export GOPATH="$work/go"
fi

report_web_update building
echo "[INFO] 编译 S2L，现有服务继续运行..."
S2L_BUILD_VERSION="${S2L_VERSION:-${S2L_REF:-source}}"
S2L_BUILD_COMMIT="${S2L_COMMIT:-}"
[[ "$S2L_BUILD_VERSION" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$ ]] || { echo "[ERROR] 无效的构建版本" >&2; exit 1; }
[[ -z "$S2L_BUILD_COMMIT" || "$S2L_BUILD_COMMIT" =~ ^[a-f0-9]{40}$ ]] || { echo "[ERROR] 无效的构建提交" >&2; exit 1; }
S2L_BUILD_FLAGS="-X github.com/lucaswren/s2l/internal/buildinfo.Version=$S2L_BUILD_VERSION -X github.com/lucaswren/s2l/internal/buildinfo.Revision=$S2L_BUILD_COMMIT"
(cd "$ROOT_DIR" && go build -ldflags "$S2L_BUILD_FLAGS" -o "$work/s2l" ./cmd/s2l)
backup="$(mktemp -d /root/s2l-backup.XXXXXXXX)"
report_web_update backup
echo "[INFO] 构建完成；短暂停止拨号并备份到 $backup ..."
systemctl is-active --quiet s2l && was_s2l=1 || true
systemctl is-active --quiet xl2tpd && was_l2tp=1 || true
systemctl is-active --quiet s2l-ipsec && was_ipsec=1 || true
changed=1
# Block PPP before removing old routes, including upgrades from unguarded versions.
"$work/s2l" -guard-only
systemctl stop xl2tpd
systemctl stop s2l
if (( was_ipsec )); then systemctl stop s2l-ipsec; fi
cp -a /opt/s2l "$backup/app"
mkdir "$backup/system"
: > "$backup/system-paths.txt"
for file in /usr/local/bin/s2l /etc/systemd/system/s2l.service /etc/systemd/system/s2l-ipsec.service /etc/ppp/chap-secrets /etc/ppp/options.xl2tpd /etc/xl2tpd/xl2tpd.conf; do
  if [[ -e "$file" || -L "$file" ]]; then
    (cd / && cp -a --parents "${file#/}" "$backup/system/")
    printf '%s\n' "$file" >> "$backup/system-paths.txt"
  fi
done
snapshot=1
iptables-save > "$backup/iptables.v4"
ip6tables-save > "$backup/iptables.v6"
printf '%s\n' "${S2L_REF:-reviewed-checkout}" > "$backup/requested-ref.txt"

report_web_update installing
install -m 755 "$work/s2l" /opt/s2l/s2l
install -m 755 "$ROOT_DIR/script/s2l.sh" /usr/local/bin/s2l
install -m 700 "$ROOT_DIR/script/remote_update.sh" /opt/s2l/remote_update.sh
for file in manage_config.py manage_ssh.py manage_ipsec.py ipsec_nat.py manage_time.py manage_kernel.py manage_update.py verify_update.py; do install -m 600 "$ROOT_DIR/script/$file" "/opt/s2l/$file"; done
for unit in s2l s2l-guard s2l-ipsec; do install -m 644 "$ROOT_DIR/deploy/systemd/$unit.service" "/etc/systemd/system/$unit.service"; done
mkdir -p /etc/systemd/system/xl2tpd.service.d
install -m 644 "$ROOT_DIR/deploy/systemd/xl2tpd-s2l.conf" /etc/systemd/system/xl2tpd.service.d/s2l.conf
report_web_update restarting
systemctl daemon-reload
systemctl enable s2l-guard.service
systemctl restart s2l-guard.service
if (( was_ipsec )); then systemctl start s2l-ipsec; systemctl is-active --quiet s2l-ipsec; fi
if (( was_l2tp )); then systemctl start xl2tpd; fi
if (( was_s2l )); then
  systemctl start s2l
  report_web_update verifying
  python3 "$ROOT_DIR/script/verify_update.py" health /opt/s2l/config.json "$backup/app/data/state.json"
  systemctl is-active --quiet s2l
fi
if (( was_l2tp )); then systemctl is-active --quiet xl2tpd; fi
systemctl is-active --quiet s2l-guard.service
complete=1
echo "[OK] S2L 升级完成；配置、账号、节点、证书和端口已保留。"
echo "[INFO] 备份：$backup；原本停止的服务保持停止。"
echo "[INFO] 运行中的客户端需重新拨号；刷新网页查看上游健康状态。"

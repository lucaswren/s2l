#!/usr/bin/env bash
# S2L installation script for Ubuntu/Debian
# 用法（在项目根目录）：
#   curl -fsSL ... | sudo bash   # 或
#   sudo bash script/install.sh
set -euo pipefail

APP_NAME="s2l"
INSTALL_DIR="/opt/s2l"
SERVICE_FILE="/etc/systemd/system/${APP_NAME}.service"
LISTEN_PORT=8080
HTTPS_PORT="${HTTPS_PORT:-}"
SINGBOX_VERSION="${SINGBOX_VERSION:-1.11.15}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

[[ "$(id -u)" -eq 0 ]] || error "请使用 root 执行：sudo bash script/install.sh"
[[ "$(uname -s)" == Linux && -d /run/systemd/system ]] || error "需要以 systemd 运行的 Ubuntu/Debian Linux 主机"
exec 9>/run/s2l-install.lock
flock -n 9 || error "另一个 S2L 安装正在运行"
info "新安装使用自签证书启用 HTTPS；管理端口未指定则随机生成，无需放行 TCP 80"

# 定位项目根目录（脚本在 script/ 下）
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

if [[ ! -f "${ROOT_DIR}/go.mod" ]]; then
  error "未找到 go.mod，请在项目根目录执行，或先把代码上传到服务器"
fi

ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64|amd64) GOARCH="amd64"; SB_ARCH="amd64" ;;
  aarch64|arm64) GOARCH="arm64"; SB_ARCH="arm64" ;;
  *) error "不支持的架构: ${ARCH}" ;;
esac

wait_for_apt() {
  local max_wait="${APT_WAIT_SEC:-600}"
  local waited=0
  command -v fuser >/dev/null 2>&1 || return 0
  while fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1 \
     || fuser /var/lib/dpkg/lock >/dev/null 2>&1 \
     || fuser /var/lib/apt/lists/lock >/dev/null 2>&1; do
    if (( waited >= max_wait )); then
      error "等待 apt 锁超时（${max_wait}s）。可执行: systemctl stop unattended-upgrades 后重试"
    fi
    if (( waited % 15 == 0 )); then
      info "系统正在自动更新，等待 apt 空闲... (${waited}s)"
    fi
    sleep 5
    waited=$((waited + 5))
  done
}

apt_get() { timeout 240s apt-get -o DPkg::Lock::Timeout=120 -o Acquire::Retries=2 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20 "$@"; }
export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=l
command -v python3 >/dev/null || { wait_for_apt; apt_get update -y; apt_get install -y --no-install-recommends python3; }
python3 "${SCRIPT_DIR}/install_state.py" recover
python3 "${SCRIPT_DIR}/install_preflight.py" system
info "1/7 安装系统依赖..."
wait_for_apt
apt_get update -y
wait_for_apt
IPSEC_PREINSTALLED=0
dpkg-query -W -f='${Status}' strongswan-starter 2>/dev/null | grep -q "install ok installed" && IPSEC_PREINSTALLED=1
apt_get install -y --no-install-recommends \
  ca-certificates curl wget tar iptables iproute2 psmisc iputils-ping mtr-tiny \
  xl2tpd ppp openssl python3 build-essential strongswan strongswan-starter kmod conntrack

# Package post-install scripts can start an unconfigured daemon. It was verified
# inactive before package installation; keep it stopped until guards are ready.
systemctl stop xl2tpd
if ! modprobe l2tp_ppp; then
  if [[ "$(. /etc/os-release; echo "$ID")" == ubuntu ]]; then
    apt_get install -y --no-install-recommends "linux-modules-extra-$(uname -r)"
  else
    error "当前内核缺少 L2TP 模块；请安装与运行内核匹配的模块并重试"
  fi
fi
python3 "${SCRIPT_DIR}/install_preflight.py" capabilities

if [[ "${IPSEC_PREINSTALLED}" == "0" ]]; then
  systemctl disable --now strongswan-starter.service >/dev/null 2>&1 || true
fi

PUBLIC_IP="$(python3 "${SCRIPT_DIR}/detect_public_ip.py")" || error "公网 IP 检测失败，请指定 PUBLIC_IP"
info "HTTPS 公网地址: ${PUBLIC_IP}"

if [[ -z "${HTTPS_PORT}" ]]; then
  HTTPS_PORT="$(python3 "${SCRIPT_DIR}/manage_config.py" random_port)"
fi
[[ "${HTTPS_PORT}" =~ ^[0-9]{1,5}$ ]] && (( 10#${HTTPS_PORT} >= 1 && 10#${HTTPS_PORT} <= 65535 )) || error "HTTPS_PORT 需为 1-65535 的整数"
HTTPS_PORT="$((10#${HTTPS_PORT}))"
info "HTTPS 端口: ${HTTPS_PORT}；请放行公网 TCP ${HTTPS_PORT}"

[[ "${HTTPS_PORT}" != "${LISTEN_PORT}" ]] || error "HTTPS_PORT 不能为 8080，该端口用于本机后端"
info "本机后端端口: ${LISTEN_PORT}"

ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-$(openssl rand -hex 8)}"
[[ "${ADMIN_USER}" =~ ^[A-Za-z0-9_.-]{1,64}$ ]] || error "ADMIN_USER 仅允许字母、数字、点、下划线和连字符"
[[ "${ADMIN_PASS}" =~ ^[A-Za-z0-9_.@+-]{12,128}$ ]] || error "ADMIN_PASS 需为 12-128 位安全字符"

info "2/7 安装 / 检查 Go..."
export PATH="/usr/local/go/bin:/usr/local/bin:${PATH:-}"
if ! command -v go >/dev/null 2>&1; then
  python3 "${SCRIPT_DIR}/download_go.py" "$GOARCH"
fi
python3 "${SCRIPT_DIR}/install_preflight.py" go "$(command -v go)"
go version

info "3/7 安装 / 检查 sing-box..."
[[ "$SINGBOX_VERSION" == 1.11.15 ]] || error "仅支持已验证的 sing-box 1.11.15"
if [[ ! -x /usr/local/bin/sing-box ]]; then
  [[ ! -e /usr/local/bin/sing-box ]] || error "sing-box 路径已被其他文件占用"
  tmp="$(mktemp -d)"
  SB_FILE="sing-box-${SINGBOX_VERSION}-linux-${SB_ARCH}.tar.gz"
  curl -fSL --retry 2 --connect-timeout 15 --max-time 180 "https://github.com/SagerNet/sing-box/releases/download/v${SINGBOX_VERSION}/${SB_FILE}" -o "${tmp}/${SB_FILE}"
  (cd "$tmp"; grep "  ${SB_FILE}$" "$SCRIPT_DIR/singbox.sha256" | sha256sum -c -)
  tar -C "$tmp" -xzf "${tmp}/${SB_FILE}"
  install -m 755 "${tmp}/sing-box-${SINGBOX_VERSION}-linux-${SB_ARCH}/sing-box" /usr/local/bin/sing-box
  rm -rf "$tmp"
fi
python3 "${SCRIPT_DIR}/install_preflight.py" singbox /usr/local/bin/sing-box

info "4/7 编译 ${APP_NAME}..."
# 前端 dist 已存在则跳过 npm；否则尝试构建
if [[ ! -f web/dist/index.html ]]; then
  if command -v npm >/dev/null 2>&1; then
    info "构建 Vue 前端..."
    (cd web && npm ci && npm run build)
  else
    error "缺少 web/dist，且未安装 npm。请先在有 Node 的机器执行 cd web && npm ci && npm run build 再上传"
  fi
fi
mkdir -p bin
S2L_BUILD_VERSION="${S2L_VERSION:-${S2L_REF:-source}}"
S2L_BUILD_COMMIT="${S2L_COMMIT:-}"
[[ "$S2L_BUILD_VERSION" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$ ]] || { echo "[ERROR] 无效的构建版本" >&2; exit 1; }
[[ -z "$S2L_BUILD_COMMIT" || "$S2L_BUILD_COMMIT" =~ ^[a-f0-9]{40}$ ]] || { echo "[ERROR] 无效的构建提交" >&2; exit 1; }
S2L_BUILD_FLAGS="-X github.com/lucaswren/s2l/internal/buildinfo.Version=$S2L_BUILD_VERSION -X github.com/lucaswren/s2l/internal/buildinfo.Revision=$S2L_BUILD_COMMIT"
go build -ldflags "$S2L_BUILD_FLAGS" -o "bin/${APP_NAME}" ./cmd/s2l
info "编译完成: ${ROOT_DIR}/bin/${APP_NAME}"

"bin/${APP_NAME}" -check-runtime
python3 "${SCRIPT_DIR}/install_state.py" begin
finish_install() {
  local code=$?
  trap - EXIT
  if (( code != 0 )); then
    python3 "${SCRIPT_DIR}/install_state.py" recover || echo "[ERROR] 自动恢复未完成，请保留 /var/lib/s2l-install 并检查恢复日志" >&2
  fi
  exit "$code"
}
trap finish_install EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# Enable system-managed clock synchronization without replacing an existing NTP provider.
if command -v timedatectl >/dev/null 2>&1; then
  if [[ "$(timedatectl show --property=CanNTP --value 2>/dev/null || true)" == "no" ]]; then
    apt_get install -y --no-install-recommends systemd-timesyncd
  fi
  timedatectl set-ntp true || warn "时间自动同步未能开启，请在设置中检查系统时间服务。"
fi

# 开启内核转发（持久）
mkdir -p /etc/sysctl.d
cat >/etc/sysctl.d/99-s2l.conf <<'EOF'
net.ipv4.ip_forward=1
net.ipv4.conf.all.rp_filter=0
net.ipv4.conf.default.rp_filter=0
EOF
sysctl --system >/dev/null 2>&1 || {
  sysctl -w net.ipv4.ip_forward=1
  sysctl -w net.ipv4.conf.all.rp_filter=0
  sysctl -w net.ipv4.conf.default.rp_filter=0
}

info "5/7 安装到 ${INSTALL_DIR}..."
mkdir -p "${INSTALL_DIR}/data"
install -m 755 "bin/${APP_NAME}" "${INSTALL_DIR}/${APP_NAME}"

cat >"${INSTALL_DIR}/config.json" <<EOF
{
  "listen": "127.0.0.1:${LISTEN_PORT}",
  "admin_user": "${ADMIN_USER}",
  "admin_pass": "${ADMIN_PASS}",
  "singbox_bin": "/usr/local/bin/sing-box",
  "data_dir": "${INSTALL_DIR}/data",
  "chap_secrets": "/etc/ppp/chap-secrets",
  "xl2tpd_conf": "/etc/xl2tpd/xl2tpd.conf",
  "ppp_options": "/etc/ppp/options.xl2tpd",
  "auto_restore": true
}
EOF
chmod 600 "${INSTALL_DIR}/config.json"
install -m 755 "${SCRIPT_DIR}/s2l.sh" /usr/local/bin/s2l
install -m 700 "${SCRIPT_DIR}/remote_update.sh" "${INSTALL_DIR}/remote_update.sh"
install -m 600 "${SCRIPT_DIR}/manage_config.py" "${INSTALL_DIR}/manage_config.py"
install -m 600 "${SCRIPT_DIR}/manage_ssh.py" "${INSTALL_DIR}/manage_ssh.py"
install -m 600 "${SCRIPT_DIR}/manage_ipsec.py" "${INSTALL_DIR}/manage_ipsec.py"
install -m 600 "${SCRIPT_DIR}/ipsec_nat.py" "${INSTALL_DIR}/ipsec_nat.py"
install -m 600 "${SCRIPT_DIR}/manage_time.py" "${INSTALL_DIR}/manage_time.py"
install -m 600 "${SCRIPT_DIR}/manage_update.py" "${INSTALL_DIR}/manage_update.py"
install -m 600 "${SCRIPT_DIR}/verify_update.py" "${INSTALL_DIR}/verify_update.py"
install -m 644 "${ROOT_DIR}/deploy/systemd/s2l-ipsec.service" /etc/systemd/system/s2l-ipsec.service

# Install fail-closed guards before allowing any PPP client to connect.
install -m 644 "${ROOT_DIR}/deploy/systemd/s2l-guard.service" /etc/systemd/system/s2l-guard.service
mkdir -p /etc/systemd/system/xl2tpd.service.d
install -m 644 "${ROOT_DIR}/deploy/systemd/xl2tpd-s2l.conf" /etc/systemd/system/xl2tpd.service.d/s2l.conf
systemctl daemon-reload
systemctl enable s2l-guard.service
systemctl restart s2l-guard.service

# 确保 chap-secrets / xl2tpd 可用（xl2tpd 只认 ; 注释，必须覆盖损坏的旧配置）
mkdir -p /etc/ppp /etc/xl2tpd /var/run/xl2tpd
touch /etc/ppp/chap-secrets
chmod 600 /etc/ppp/chap-secrets

cat >/etc/ppp/options.xl2tpd <<'EOF'
refuse-eap
refuse-pap
require-chap
require-mschap-v2
noccp
auth
proxyarp
nodefaultroute
mtu 1400
mru 1400
lcp-echo-interval 30
lcp-echo-failure 4
connect-delay 5000
ms-dns 1.1.1.1
ms-dns 8.8.8.8
EOF

cat >/etc/xl2tpd/xl2tpd.conf <<'EOF'
; BEGIN s2l managed block - DO NOT EDIT
; generated by s2l install - comments must use ';'
[global]
port = 1701
auth file = /etc/ppp/chap-secrets
access control = no

[lns default]
ip range = 10.0.0.2-10.0.255.254
local ip = 10.255.254.1
require chap = yes
refuse pap = yes
require authentication = yes
name = s2l
pppoptfile = /etc/ppp/options.xl2tpd
length bit = yes
; END s2l managed block
EOF

systemctl enable xl2tpd >/dev/null 2>&1 || true
# 清理可能残留的旧进程后再启动
pkill -x xl2tpd >/dev/null 2>&1 || true
sleep 1
if systemctl restart xl2tpd >/dev/null 2>&1 && systemctl is-active --quiet xl2tpd; then
  info "xl2tpd 已启动"
else
  error "xl2tpd 启动失败，请检查: systemctl status xl2tpd -l --no-pager"
fi

info "6/7 写入 systemd 服务..."
install -m 644 "${ROOT_DIR}/deploy/systemd/s2l.service" "${SERVICE_FILE}"

systemctl daemon-reload
systemctl enable "${APP_NAME}"
systemctl restart "${APP_NAME}"

info "7/7 检查服务状态..."
# Type=simple being active does not mean initialization or HTTP binding finished.
if python3 "${SCRIPT_DIR}/verify_update.py" health "${INSTALL_DIR}/config.json"; then
  info "生成自签证书并启用 HTTPS..."
  # Execute as a separate command so setup_https.sh retains its ERR rollback trap.
  # Never report installation success while the management endpoint lacks TLS.
  trap 'error "HTTPS 配置失败，正在恢复安装前状态"' ERR
  bash "${SCRIPT_DIR}/setup_https.sh" "${PUBLIC_IP}" "${HTTPS_PORT}"
  trap - ERR
  echo
  python3 "${SCRIPT_DIR}/verify_update.py" health "${INSTALL_DIR}/config.json"
  systemctl is-active --quiet xl2tpd
  python3 "${SCRIPT_DIR}/install_state.py" commit
  info "S2L 安装完成"
  echo "  Web UI : https://${PUBLIC_IP}:${HTTPS_PORT}"
  echo "  管理账号: ${ADMIN_USER}"
  echo "  管理密码: ${ADMIN_PASS}"
  echo "  配置   : ${INSTALL_DIR}/config.json"
  echo "  数据   : ${INSTALL_DIR}/data/state.json"
  echo "  日志   : journalctl -u ${APP_NAME} -f"
  echo "  地址与证书信息: s2l 1"
  echo
  echo "常用命令："
  echo "  systemctl status ${APP_NAME}"
  echo "  systemctl restart ${APP_NAME}"
  echo "  systemctl stop ${APP_NAME}"
else
  warn "服务未处于 active，请查看日志："
  echo "  journalctl -u ${APP_NAME} -n 80 --no-pager"
  systemctl status "${APP_NAME}" --no-pager || true
  exit 1
fi

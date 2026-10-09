#!/usr/bin/env bash
# s2l 一键安装/启动脚本（Ubuntu/Debian）
# 用法（在项目根目录）：
#   curl -fsSL ... | sudo bash   # 或
#   sudo bash script/install.sh
set -euo pipefail

APP_NAME="s2l"
INSTALL_DIR="/opt/s2l"
SERVICE_FILE="/etc/systemd/system/${APP_NAME}.service"
LISTEN_PORT="${LISTEN_PORT:-}"
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
[[ ! -e "${INSTALL_DIR}/config.json" ]] || error "s2l 已安装；为保护现有账号和配置，请使用代码更新流程，勿重复执行安装脚本"
info "新安装默认启用 HTTPS；TCP 80 用于证书申请与续期；HTTPS 使用自定义端口，未指定则随机生成"

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
  while fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1 \
     || fuser /var/lib/dpkg/lock >/dev/null 2>&1 \
     || fuser /var/lib/apt/lists/lock >/dev/null 2>&1 \
     || pgrep -x unattended-upgr >/dev/null 2>&1 \
     || pgrep -x apt-get >/dev/null 2>&1 \
     || pgrep -x apt >/dev/null 2>&1; do
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

info "1/7 安装系统依赖..."
export DEBIAN_FRONTEND=noninteractive
# 确保 fuser 可用（来自 psmisc）
command -v fuser >/dev/null 2>&1 || true
wait_for_apt
apt-get update -y
wait_for_apt
IPSEC_PREINSTALLED=0
dpkg-query -W -f='${Status}' strongswan-starter 2>/dev/null | grep -q "install ok installed" && IPSEC_PREINSTALLED=1
apt-get install -y --no-install-recommends \
  ca-certificates curl wget tar iptables iproute2 psmisc \
  xl2tpd ppp openssl python3 build-essential strongswan strongswan-starter

if [[ "${IPSEC_PREINSTALLED}" == "0" ]]; then
  systemctl disable --now strongswan-starter.service >/dev/null 2>&1 || true
fi

PUBLIC_IP="$(python3 "${SCRIPT_DIR}/detect_public_ip.py")" || error "公网 IP 检测失败，请指定 PUBLIC_IP"
info "HTTPS 公网地址: ${PUBLIC_IP}"

if [[ -z "${HTTPS_PORT}" ]]; then
  HTTPS_PORT="$(python3 "${SCRIPT_DIR}/manage_config.py" random_port)"
fi
[[ "${HTTPS_PORT}" =~ ^[0-9]{1,5}$ ]] && (( 10#${HTTPS_PORT} >= 1 && 10#${HTTPS_PORT} <= 65535 && 10#${HTTPS_PORT} != 80 )) || error "HTTPS_PORT 需为 1-65535 的整数，不能为 80"
HTTPS_PORT="$((10#${HTTPS_PORT}))"
info "HTTPS 端口: ${HTTPS_PORT}；请放行公网 TCP 80、${HTTPS_PORT}"

if [[ -z "${LISTEN_PORT}" ]]; then
  LISTEN_PORT="$(python3 "${SCRIPT_DIR}/manage_config.py" random_port)"
  while [[ "${LISTEN_PORT}" == "${HTTPS_PORT}" ]]; do
    LISTEN_PORT="$(python3 "${SCRIPT_DIR}/manage_config.py" random_port)"
  done
fi
[[ "${LISTEN_PORT}" =~ ^[0-9]{1,5}$ ]] && (( 10#${LISTEN_PORT} >= 1 && 10#${LISTEN_PORT} <= 65535 )) || error "LISTEN_PORT 需为 1-65535 的整数"
LISTEN_PORT="$((10#${LISTEN_PORT}))"
[[ "${LISTEN_PORT}" != "80" && "${LISTEN_PORT}" != "${HTTPS_PORT}" ]] || error "本机后端端口不能为 80 或与 HTTPS 端口相同"
info "本机后端端口: ${LISTEN_PORT}"

ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-$(openssl rand -hex 8)}"
[[ "${ADMIN_USER}" =~ ^[A-Za-z0-9_.-]{1,64}$ ]] || error "ADMIN_USER 仅允许字母、数字、点、下划线和连字符"
[[ "${ADMIN_PASS}" =~ ^[A-Za-z0-9_.@+-]{12,128}$ ]] || error "ADMIN_PASS 需为 12-128 位安全字符"

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

info "2/7 安装 / 检查 Go..."
if ! command -v go >/dev/null 2>&1; then
  GO_VER="1.22.12"
  tmp="$(mktemp -d)"
  wget -qO "${tmp}/go.tgz" "https://go.dev/dl/go${GO_VER}.linux-${GOARCH}.tar.gz"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "${tmp}/go.tgz"
  rm -rf "${tmp}"
  export PATH="/usr/local/go/bin:${PATH}"
  if ! grep -q '/usr/local/go/bin' /etc/profile.d/go.sh 2>/dev/null; then
    echo 'export PATH=/usr/local/go/bin:$PATH' >/etc/profile.d/go.sh
  fi
fi
export PATH="/usr/local/go/bin:${PATH:-}"
go version

info "3/7 安装 / 检查 sing-box..."
if [[ ! -x /usr/local/bin/sing-box ]]; then
  tmp="$(mktemp -d)"
  SB_URL="https://github.com/SagerNet/sing-box/releases/download/v${SINGBOX_VERSION}/sing-box-${SINGBOX_VERSION}-linux-${SB_ARCH}.tar.gz"
  info "下载 sing-box ${SINGBOX_VERSION} ..."
  wget -qO "${tmp}/sb.tgz" "${SB_URL}" || error "下载 sing-box 失败，请检查网络或手动安装到 /usr/local/bin/sing-box"
  tar -C "${tmp}" -xzf "${tmp}/sb.tgz"
  install -m 755 "${tmp}/sing-box-${SINGBOX_VERSION}-linux-${SB_ARCH}/sing-box" /usr/local/bin/sing-box
  rm -rf "${tmp}"
fi
sing-box version || true

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
go build -o "bin/${APP_NAME}" ./cmd/s2l
info "编译完成: ${ROOT_DIR}/bin/${APP_NAME}"

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
install -m 600 "${SCRIPT_DIR}/manage_config.py" "${INSTALL_DIR}/manage_config.py"
install -m 600 "${SCRIPT_DIR}/manage_ssh.py" "${INSTALL_DIR}/manage_ssh.py"
install -m 600 "${SCRIPT_DIR}/manage_ipsec.py" "${INSTALL_DIR}/manage_ipsec.py"
install -m 644 "${ROOT_DIR}/deploy/systemd/s2l-ipsec.service" /etc/systemd/system/s2l-ipsec.service

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
  warn "xl2tpd 启动失败，请检查: systemctl status xl2tpd -l --no-pager"
  warn "配置文件: /etc/xl2tpd/xl2tpd.conf"
fi

info "6/7 写入 systemd 服务..."
install -m 644 "${ROOT_DIR}/deploy/systemd/s2l.service" "${SERVICE_FILE}"

systemctl daemon-reload
systemctl enable "${APP_NAME}"
systemctl restart "${APP_NAME}"

info "7/7 检查服务状态..."
sleep 1
if systemctl is-active --quiet "${APP_NAME}"; then
  info "申请公网 IP 证书并启用 HTTPS..."
  # Execute as a separate command so setup_https.sh retains its ERR rollback trap.
  # Never report installation success while the management endpoint lacks TLS.
  trap 'error "HTTPS 尚未完成；请放行 TCP 80、${HTTPS_PORT} 后执行 bash ${SCRIPT_DIR}/setup_https.sh ${PUBLIC_IP} ${HTTPS_PORT}；管理配置已保留于 ${INSTALL_DIR}/config.json"' ERR
  bash "${SCRIPT_DIR}/setup_https.sh" "${PUBLIC_IP}" "${HTTPS_PORT}"
  trap - ERR
  echo
  info "安装成功！"
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

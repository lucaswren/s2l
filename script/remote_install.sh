#!/usr/bin/env bash
# Fetch and install S2L from GitHub on Ubuntu/Debian
#
#   curl -fsSL https://raw.githubusercontent.com/lucaswren/s2l/main/script/remote_install.sh | sudo bash
#
# 可选环境变量：
#   REPO_URL=https://github.com/lucaswren/s2l.git
#   BRANCH=main
#   HTTPS_PORT=8443 # 不指定则随机生成 HTTPS 端口
#   PUBLIC_IP=1.2.3.4 # 默认自动检测公网 IPv4
set -euo pipefail

REPO_URL="${REPO_URL:-https://github.com/lucaswren/s2l.git}"
BRANCH="${BRANCH:-}"
SRC_DIR="${SRC_DIR:-/opt/src/s2l}"

[[ "$(id -u)" -eq 0 ]] || { echo "请使用 root：curl ... | sudo bash"; exit 1; }

wait_for_apt() {
  local max_wait="${APT_WAIT_SEC:-600}" # 默认最多等 10 分钟
  local waited=0
  echo "[INFO] 检查 apt 锁..."
  command -v fuser >/dev/null 2>&1 || return 0
  while fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1 \
     || fuser /var/lib/dpkg/lock >/dev/null 2>&1 \
     || fuser /var/lib/apt/lists/lock >/dev/null 2>&1; do
    if (( waited >= max_wait )); then
      echo "[ERROR] 等待 apt 锁超时（${max_wait}s）。可稍后重试，或执行: systemctl stop unattended-upgrades"
      exit 1
    fi
    if (( waited % 15 == 0 )); then
      echo "[INFO] 系统正在自动更新，等待 apt 空闲... (${waited}s)"
    fi
    sleep 5
    waited=$((waited + 5))
  done
}

export DEBIAN_FRONTEND=noninteractive
wait_for_apt
timeout 240s apt-get -o DPkg::Lock::Timeout=120 -o Acquire::Retries=2 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20 update -y
wait_for_apt
timeout 240s apt-get -o DPkg::Lock::Timeout=120 -o Acquire::Retries=2 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20 install -y --no-install-recommends ca-certificates curl git python3

if [[ -z "$BRANCH" ]]; then
  [[ "$REPO_URL" == https://github.com/lucaswren/s2l.git ]] || { echo "自定义仓库需指定 BRANCH"; exit 1; }
  BRANCH="$(curl -fsSL --retry 2 --connect-timeout 15 --max-time 30 https://api.github.com/repos/lucaswren/s2l/releases/latest | python3 -c 'import json,sys,re; tag=json.load(sys.stdin)["tag_name"]; assert re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+",tag); print(tag)')"
fi
[[ "$BRANCH" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$ ]] || { echo "无效的 BRANCH"; exit 1; }
if [[ -d "${SRC_DIR}/.git" ]]; then
  echo "[INFO] 更新代码 ${SRC_DIR} ..."
  git -C "${SRC_DIR}" fetch --depth 1 origin "${BRANCH}"
  git -C "${SRC_DIR}" checkout --detach FETCH_HEAD
else
  echo "[INFO] 克隆 ${REPO_URL} ..."
  if [[ -e "${SRC_DIR}" ]]; then
    echo "[ERROR] ${SRC_DIR} 已存在但不是 Git 仓库；为保护现有文件，安装已停止。设置 SRC_DIR 到新的空目录后重试。"
    exit 1
  fi
  mkdir -p "$(dirname "${SRC_DIR}")"
  git clone --depth 1 -b "${BRANCH}" "${REPO_URL}" "${SRC_DIR}"
fi

export S2L_REF="$BRANCH"
export S2L_COMMIT="$(git -C "$SRC_DIR" rev-parse HEAD)"
exec bash "${SRC_DIR}/script/install.sh"

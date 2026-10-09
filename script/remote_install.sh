#!/usr/bin/env bash
# 一条命令从 GitHub 安装并启动 s2l（Ubuntu/Debian）
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
BRANCH="${BRANCH:-main}"
SRC_DIR="${SRC_DIR:-/opt/src/s2l}"

[[ "$(id -u)" -eq 0 ]] || { echo "请使用 root：curl ... | sudo bash"; exit 1; }

wait_for_apt() {
  local max_wait="${APT_WAIT_SEC:-600}" # 默认最多等 10 分钟
  local waited=0
  echo "[INFO] 检查 apt 锁..."
  while fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1 \
     || fuser /var/lib/dpkg/lock >/dev/null 2>&1 \
     || fuser /var/lib/apt/lists/lock >/dev/null 2>&1 \
     || pgrep -x unattended-upgr >/dev/null 2>&1 \
     || pgrep -x apt-get >/dev/null 2>&1 \
     || pgrep -x apt >/dev/null 2>&1; do
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
apt-get update -y
wait_for_apt
apt-get install -y --no-install-recommends ca-certificates curl git

if [[ -d "${SRC_DIR}/.git" ]]; then
  echo "[INFO] 更新代码 ${SRC_DIR} ..."
  git -C "${SRC_DIR}" fetch --depth 1 origin "${BRANCH}"
  git -C "${SRC_DIR}" checkout -B "${BRANCH}" "origin/${BRANCH}"
else
  echo "[INFO] 克隆 ${REPO_URL} ..."
  if [[ -e "${SRC_DIR}" ]]; then
    echo "[ERROR] ${SRC_DIR} 已存在但不是 Git 仓库；为保护现有文件，安装已停止。设置 SRC_DIR 到新的空目录后重试。"
    exit 1
  fi
  mkdir -p "$(dirname "${SRC_DIR}")"
  git clone --depth 1 -b "${BRANCH}" "${REPO_URL}" "${SRC_DIR}"
fi

exec bash "${SRC_DIR}/script/install.sh"

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
#   S2L_KERNEL_UPGRADE=auto # 已知问题内核自动准备 HWE；off 则仅拒绝安装
# 准备内核后退出 20；不会重启。人工重启后使用原参数重跑此命令。
set -euo pipefail

# Shared by the local installer. Keep bootstrap language handling self-contained
# so --help and invalid options never download files or install packages.
s2l_text() {
  case "${_S2L_INSTALL_LANG:-zh-Hans}" in
    en) printf '%s\n' "$1" ;;
    zh-Hant) printf '%s\n' "$2" ;;
    zh-Hans) printf '%s\n' "$3" ;;
  esac
}
s2l_language() {
  _S2L_INSTALL_LANG=zh-Hans
  local selected=0 help=0
  while (( $# )); do
    case "$1" in
      --lang|--lang=*)
        if (( selected )); then echo '--lang may only be specified once (en | zh-Hant | zh-Hans)' >&2; return 2; fi
        selected=1
        if [[ "$1" == --lang ]]; then
          (( $# >= 2 )) || { echo '--lang requires en | zh-Hant | zh-Hans' >&2; return 2; }
          shift; _S2L_INSTALL_LANG="$1"
        else _S2L_INSTALL_LANG="${1#--lang=}"; fi
        case "$_S2L_INSTALL_LANG" in en|zh-Hant|zh-Hans) ;; *) echo 'Invalid --lang: en | zh-Hant | zh-Hans' >&2; return 2;; esac ;;
      -h|--help) help=1 ;;
      *) s2l_text "Unknown argument: $1" "未知參數：$1" "未知参数：$1" >&2; return 2 ;;
    esac
    shift
  done
  export _S2L_INSTALL_LANG
  if (( help )); then
    s2l_text 'Usage: bash install.sh [--lang en|zh-Hant|zh-Hans]' '用法：bash install.sh [--lang en|zh-Hant|zh-Hans]' '用法：bash install.sh [--lang en|zh-Hant|zh-Hans]'
    s2l_text 'Languages: English, Traditional Chinese, Simplified Chinese (default).' '語言：English、繁體中文、简体中文（預設）。' '语言：English、繁體中文、简体中文（默认）。'
    return 10
  fi
}
s2l_stage() { S2L_STAGE_START=$SECONDS; s2l_text "[INFO] $1..." "[INFO] $2..." "[INFO] $3..."; }
s2l_elapsed() { s2l_text "[TIME] $1: $((SECONDS - S2L_STAGE_START))s" "[TIME] $2：$((SECONDS - S2L_STAGE_START))s" "[TIME] $3：$((SECONDS - S2L_STAGE_START))s"; }
# Sourcing this file only loads the shared functions; no bootstrap actions run.
if [[ -n "${BASH_SOURCE[0]:-}" && "${BASH_SOURCE[0]}" != "$0" ]]; then return 0; fi
language_result=0
s2l_language "$@" || language_result=$?
if (( language_result )); then
  if (( language_result == 10 )); then exit 0; else exit "$language_result"; fi
fi

REPO_URL="${REPO_URL:-https://github.com/lucaswren/s2l.git}"
BRANCH="${BRANCH:-}"
SRC_DIR="${SRC_DIR:-/opt/src/s2l}"

[[ "$(id -u)" -eq 0 ]] || { echo "$(s2l_text "Please run as root: curl ... | sudo bash" "請使用 root：curl ... | sudo bash" "请使用 root：curl ... | sudo bash")"; exit 1; }

wait_for_apt() {
  local max_wait="${APT_WAIT_SEC:-600}" # 默认最多等 10 分钟
  local waited=0
  echo "[INFO] $(s2l_text "Checking APT locks..." "檢查 APT 鎖定..." "检查 apt 锁...")"
  command -v fuser >/dev/null 2>&1 || return 0
  while fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1 \
     || fuser /var/lib/dpkg/lock >/dev/null 2>&1 \
     || fuser /var/lib/apt/lists/lock >/dev/null 2>&1; do
    if (( waited >= max_wait )); then
      echo "[ERROR] $(s2l_text "APT lock wait timed out (${max_wait}s). Check the package manager and retry later." "等待 APT 鎖定逾時（${max_wait}s）。請檢查套件管理器後重試。" "等待 apt 锁超时（${max_wait}s）。可稍后重试，或执行: systemctl stop unattended-upgrades")"
      exit 1
    fi
    if (( waited % 15 == 0 )); then
      echo "[INFO] $(s2l_text "Waiting for the package manager... (${waited}s)" "等待套件管理器空閒... (${waited}s)" "系统正在自动更新，等待 apt 空闲... (${waited}s)")"
    fi
    sleep 5
    waited=$((waited + 5))
  done
}

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=l
wait_for_apt
timeout 240s apt-get -o DPkg::Lock::Timeout=120 -o Acquire::Retries=2 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20 update -y
wait_for_apt
timeout 240s apt-get -o DPkg::Lock::Timeout=120 -o Acquire::Retries=2 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20 install -y --no-install-recommends ca-certificates curl git python3

if [[ -z "$BRANCH" ]]; then
  [[ "$REPO_URL" == https://github.com/lucaswren/s2l.git ]] || { echo "$(s2l_text "Custom repositories require BRANCH" "自訂儲存庫需指定 BRANCH" "自定义仓库需指定 BRANCH")"; exit 1; }
  BRANCH="$(curl -fsSL --retry 2 --connect-timeout 15 --max-time 30 https://api.github.com/repos/lucaswren/s2l/releases/latest | python3 -c 'import json,sys,re; tag=json.load(sys.stdin)["tag_name"]; assert re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+",tag); print(tag)')"
fi
[[ "$BRANCH" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$ ]] || { echo "$(s2l_text "Invalid BRANCH" "無效的 BRANCH" "无效的 BRANCH")"; exit 1; }
if [[ -d "${SRC_DIR}/.git" ]]; then
  echo "[INFO] $(s2l_text "Updating source in ${SRC_DIR} ..." "更新程式碼 ${SRC_DIR} ..." "更新代码 ${SRC_DIR} ...")"
  git -C "${SRC_DIR}" fetch --depth 1 origin "${BRANCH}"
  git -C "${SRC_DIR}" checkout --detach FETCH_HEAD
else
  echo "[INFO] $(s2l_text "Cloning ${REPO_URL} ..." "複製 ${REPO_URL} ..." "克隆 ${REPO_URL} ...")"
  if [[ -e "${SRC_DIR}" ]]; then
    echo "[ERROR] $(s2l_text "${SRC_DIR} exists and is not a Git repository. Set SRC_DIR to a new empty directory and retry." "${SRC_DIR} 已存在但不是 Git 儲存庫；請將 SRC_DIR 設為新的空目錄後重試。" "${SRC_DIR} 已存在但不是 Git 仓库；为保护现有文件，安装已停止。设置 SRC_DIR 到新的空目录后重试。")"
    exit 1
  fi
  mkdir -p "$(dirname "${SRC_DIR}")"
  git clone --depth 1 -b "${BRANCH}" "${REPO_URL}" "${SRC_DIR}"
fi

export S2L_REF="$BRANCH"
export S2L_COMMIT="$(git -C "$SRC_DIR" rev-parse HEAD)"
exec bash "${SRC_DIR}/script/install.sh" --lang "$_S2L_INSTALL_LANG"

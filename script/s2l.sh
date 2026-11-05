#!/usr/bin/env bash
set -euo pipefail

APP_NAME="s2l"
CONFIG_FILE="/opt/s2l/config.json"
I18N_FILE="$(dirname -- "${BASH_SOURCE[0]}")/installer_i18n.py"
if [[ ! -f "$I18N_FILE" ]]; then I18N_FILE="/opt/s2l/installer_i18n.py"; fi
declare -A CLI_TEXT=()
load_language() {
  local key value
  CLI_TEXT=()
  while IFS= read -r -d '' key && IFS= read -r -d '' value; do
    CLI_TEXT["$key"]="$value"
  done < <(python3 "$I18N_FILE" --cli "$CONFIG_FILE")
  case "${CLI_TEXT[_language]:-}" in
    en|zh-Hant|zh-Hans) export _S2L_INSTALL_LANG="${CLI_TEXT[_language]}" ;;
    *) echo '[ERROR] Cannot load S2L language resources.' >&2; return 1 ;;
  esac
}
t() { printf '%s' "${CLI_TEXT[$1]:-$1}"; }
load_language
DIRECT=0
valid_arguments=1
if (( $# > 2 )); then valid_arguments=0; fi
if (( $# == 2 )); then
  case "$1:$2" in
    uninstall:--dry-run|13:--dry-run|language:en|language:zh-Hant|language:zh-Hans|14:en|14:zh-Hant|14:zh-Hans) ;;
    *) valid_arguments=0 ;;
  esac
fi
if (( ! valid_arguments )); then
  echo "$(t '用法：s2l [0-14|kernel-upgrade|uninstall]；s2l uninstall --dry-run 只读预览')" >&2
  exit 2
fi
if (( $# >= 1 )); then
  case "$1" in
    -h|--help) echo "$(t '用法：s2l 打开菜单；s2l [0-14] 执行对应操作；s2l kernel-upgrade 准备内核（不自动重启）；s2l uninstall --dry-run 只读预览卸载。')"; echo "s2l language [en|zh-Hant|zh-Hans]"; exit 0 ;;
    0|1|2|3|4|5|6|7|8|9|10|11|12|13|14|language|kernel-upgrade|uninstall) DIRECT=1 ;;
    *) echo "$(t '无效选项：')$1$(t '；使用 s2l --help 查看用法。')" >&2; exit 2 ;;
  esac
fi

if [[ "$(id -u)" -ne 0 ]]; then
  if command -v sudo >/dev/null 2>&1; then
    exec sudo "$0" "$@"
  fi
  echo "$(t '请以 root 执行：sudo s2l')" >&2
  exit 1
fi

pause() {
  if (( DIRECT )); then return; fi
  echo
  read -r -p "$(t '按回车返回菜单...')" _
}

show_status() {
  systemctl status "${APP_NAME}" --no-pager || true
  pause
}

show_logs() {
  journalctl -u "${APP_NAME}" -n 80 --no-pager || true
  pause
}

show_url() {
  if [[ ! -r "${CONFIG_FILE}" ]]; then
    echo "$(t '未找到配置文件：')${CONFIG_FILE}"
    pause
    return 1
  fi
  python3 - "${CONFIG_FILE}" <<'PY'
import json
from pathlib import Path
import ipaddress
import subprocess
import sys
sys.path.insert(0, str(Path(sys.argv[1]).parent))
from installer_i18n import tr
import urllib.request
from urllib.parse import urlsplit

def public_ip():
    try:
        with urllib.request.urlopen('https://api.ipify.org', timeout=3) as response:
            value = response.read(64).decode().strip()
        address = ipaddress.ip_address(value)
        if address.is_global:
            return str(address), False
    except (OSError, ValueError):
        pass
    try:
        result = subprocess.run(['ip', '-j', '-4', 'route', 'get', '1.1.1.1'], capture_output=True, text=True, timeout=3, check=True)
        address = json.loads(result.stdout)[0].get('prefsrc')
        if address:
            return str(ipaddress.ip_address(address)), True
    except (OSError, ValueError, IndexError, subprocess.SubprocessError):
        pass
    return None, True

try:
    with open(sys.argv[1], encoding="utf-8") as config_file:
        config = json.load(config_file)
    listen = config.get("listen", ":8080")
    host, separator, port = listen.rpartition(':')
    if not separator or not port.isdigit() or not 1 <= int(port) <= 65535:
        raise ValueError(tr('管理监听地址格式错误'))
    host = host.strip('[]')
    external = config.get('public_url', '')
    parsed = urlsplit(external)
    if external and parsed.scheme in ('http', 'https') and parsed.hostname and not parsed.username and not parsed.password:
        print(tr("网页管理地址：{address}").format(address=external))
    else:
        inferred = False
        if host in ('', '0.0.0.0', '::'):
            host, inferred = public_ip()
        if host:
            address = f'[{host}]' if ':' in host else host
            print(tr("网页管理地址：{address}").format(address=f"http://{address}:{port}"))
            if inferred:
                print(tr('未能查询公网地址，以上为本机出口地址；存在 NAT 时请使用实际公网 IP。'))
        else:
            print(tr('无法自动识别服务器地址，请访问 http://服务器公网IP:{port}').format(port=port))
        if host in ('127.0.0.1', '::1', 'localhost'):
            print(tr('管理服务仅在本机监听，请通过 SSH 隧道或反向代理访问。'))
    if config.get('https_certificate') == 'self_signed':
        print(tr('证书类型：自签证书，浏览器会显示信任警告；证书位于 /opt/s2l/tls/self-signed.crt。'))
    print(tr("管理监听地址：{listen}").format(listen=listen))
    print(tr("管理用户名：{user}").format(user=config.get("admin_user") or tr("未启用认证")))
    print(tr('管理密码：不在命令输出中显示。'))
except (OSError, json.JSONDecodeError) as error:
    print(tr("读取配置失败：{error}").format(error=error))
    sys.exit(1)
except ValueError as error:
    print(tr("配置错误：{error}").format(error=error))
    sys.exit(1)
PY
  pause
}

change_setting() {
  local field="$1" value="" repeated="" result=0
  echo "$(t '保存后将重启服务，当前拨号连接会中断；留空取消。')"
  case "${field}" in
    admin_user) read -r -p "$(t '新管理用户名: ')" value ;;
    admin_pass)
      read -r -s -p "$(t '新管理密码（12-128 位）: ')" value
      echo
      if [[ -n "${value}" ]]; then
        read -r -s -p "$(t '再次输入密码: ')" repeated
        echo
        if [[ "${value}" != "${repeated}" ]]; then
          echo "$(t '两次密码不一致，未修改。')"
          pause
          return
        fi
      fi
      ;;
    listen_port)
      echo "$(t '请先放行新 TCP 端口；HTTPS 部署将修改 Nginx 端口，保留现有证书。')"
      read -r -p "$(t '新网页端口（1-65535）: ')" value
      ;;
  esac
  if [[ -n "${value}" ]]; then
      printf '%s' "${value}" | python3 /opt/s2l/manage_config.py "${field}" || result=$?
  fi
  pause
  return "${result}"
}

change_ssh_port() {
  local port="" confirmed="" result=0
  echo "$(t '请先在安全组/防火墙放行新 SSH 端口，并保持当前连接。')"
  echo "$(t '旧端口将停止接受新连接；修改后需在另一个终端验证登录。')"
  read -r -p "$(t '新 SSH 端口（1-65535，留空取消）: ')" port
  if [[ -n "${port}" ]]; then
    read -r -p "$(t '已放行新端口并准备验证？输入 yes 继续: ')" confirmed
    if [[ "${confirmed}" == "yes" ]]; then
      printf '%s' "${port}" | python3 /opt/s2l/manage_ssh.py || result=$?
    else
      echo "$(t '已取消')"
    fi
  fi
  pause
  return "${result}"
}

change_language() {
  local selected="${1:-}" choice="" result=0
  if [[ -z "$selected" ]]; then
    echo "$(t '语言')"
    echo "1) English"
    echo "2) 繁體中文"
    echo "3) 简体中文"
    echo "0) $(t '退出')"
    read -r -p "$(t '选择语言 [1-3，0 取消]: ')" choice
    case "$choice" in
      1) selected=en ;;
      2) selected=zh-Hant ;;
      3) selected=zh-Hans ;;
      0|"") return 0 ;;
      *) echo "$(t '语言无效，请选择 en、zh-Hant 或 zh-Hans')"; pause; return 2 ;;
    esac
  fi
  printf '%s' "$selected" | python3 /opt/s2l/manage_config.py ui_language || result=$?
  if (( result == 0 )); then load_language; echo "$(t '语言设置已保存，Web 刷新后同步。')"; fi
  pause
  return "$result"
}

service_action() {
  local action="$1" message="$2" result=0
  systemctl "${action}" "${APP_NAME}" && echo "${message}" || result=$?
  pause
  return "${result}"
}

dispatch() {
  case "$1" in
    1) show_url ;;
    2) show_status ;;
    3) service_action restart "$(t 'S2L 已重启')" ;;
    4) show_logs ;;
    5) service_action start "$(t 'S2L 已启动')" ;;
    6) service_action stop "$(t 'S2L 已停止')" ;;
    7) change_setting admin_pass ;;
    8) change_setting admin_user ;;
    9) change_setting listen_port ;;
    10) change_ssh_port ;;
    11) local result=0; bash /opt/s2l/remote_update.sh || result=$?; pause; return "$result" ;;
    12|kernel-upgrade) local result=0; python3 /opt/s2l/manage_kernel.py --upgrade || result=$?; pause; return "$result" ;;
    13|uninstall) exec python3 /opt/s2l/uninstall.py --lang "$_S2L_INSTALL_LANG" "${@:2}" ;;
    14|language) change_language "${2:-}" ;;
    0) exit 0 ;;
    *) echo "$(t '无效选项')"; pause ;;
  esac
}

if (( DIRECT )); then
  dispatch "$@"
  exit $?
fi

while true; do
  load_language
  clear || true
  echo "$(t 'S2L 管理')"
  echo "────────────────────"
  echo "$(t '服务')"
  echo "1) $(t '管理地址')"
  echo "2) $(t '服务状态')"
  echo "3) $(t '重启服务')"
  echo "4) $(t '服务日志')"
  echo "5) $(t '启动服务')"
  echo "6) $(t '停止服务')"
  echo "$(t '管理')"
  echo "7) $(t '管理密码')"
  echo "8) $(t '管理用户名')"
  echo "9) $(t '管理端口')"
  echo "10) $(t 'SSH 端口')"
  echo "$(t '维护')"
  echo "11) $(t '软件更新')"
  echo "12) $(t '内核维护')"
  echo "13) $(t '卸载 S2L')"
  echo "14) $(t '语言')"
  echo "0) $(t '退出')"
  echo
  read -r -p "$(t '请选择操作 [0-14]: ')" choice
  dispatch "${choice}" || true
done

#!/usr/bin/env bash
set -euo pipefail

APP_NAME="s2l"
CONFIG_FILE="/opt/s2l/config.json"
DIRECT=0
if (( $# > 1 )); then
  echo "用法：s2l [0-10]" >&2
  exit 2
fi
if (( $# == 1 )); then
  case "$1" in
    -h|--help) echo "用法：s2l 打开菜单；s2l 1 查看地址；s2l 2 查看状态；s2l 4 查看日志；其余编号与菜单一致。"; exit 0 ;;
    0|1|2|3|4|5|6|7|8|9|10) DIRECT=1 ;;
    *) echo "无效编号：$1；使用 s2l --help 查看用法。" >&2; exit 2 ;;
  esac
fi

if [[ "$(id -u)" -ne 0 ]]; then
  if command -v sudo >/dev/null 2>&1; then
    exec sudo "$0" "$@"
  fi
  echo "请以 root 执行：sudo s2l" >&2
  exit 1
fi

pause() {
  if (( DIRECT )); then return; fi
  echo
  read -r -p "按回车返回菜单..." _
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
    echo "未找到配置文件：${CONFIG_FILE}"
    pause
    return 1
  fi
  python3 - "${CONFIG_FILE}" <<'PY'
import json
import ipaddress
import subprocess
import sys
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
        raise ValueError('管理监听地址格式错误')
    host = host.strip('[]')
    external = config.get('public_url', '')
    parsed = urlsplit(external)
    if external and parsed.scheme in ('http', 'https') and parsed.hostname and not parsed.username and not parsed.password:
        print(f"网页管理地址：{external}")
    else:
        inferred = False
        if host in ('', '0.0.0.0', '::'):
            host, inferred = public_ip()
        if host:
            address = f'[{host}]' if ':' in host else host
            print(f"网页管理地址：http://{address}:{port}")
            if inferred:
                print('未能查询公网地址，以上为本机出口地址；存在 NAT 时请使用实际公网 IP。')
        else:
            print(f'无法自动识别服务器地址，请访问 http://服务器公网IP:{port}')
        if host in ('127.0.0.1', '::1', 'localhost'):
            print('管理服务仅在本机监听，请通过 SSH 隧道或反向代理访问。')
    if config.get('https_certificate') == 'self_signed':
        print('证书类型：自签证书，浏览器会显示信任警告；证书位于 /opt/s2l/tls/self-signed.crt。')
    print(f"管理监听地址：{listen}")
    print(f"管理用户名：{config.get('admin_user') or '未启用认证'}")
    print('管理密码：不在命令输出中显示。')
except (OSError, json.JSONDecodeError) as error:
    print(f"读取配置失败：{error}")
    sys.exit(1)
except ValueError as error:
    print(f"配置错误：{error}")
    sys.exit(1)
PY
  pause
}

change_setting() {
  local field="$1" value="" repeated="" result=0
  echo "保存后将重启服务，当前拨号连接会中断；留空取消。"
  case "${field}" in
    admin_user) read -r -p "新管理用户名: " value ;;
    admin_pass)
      read -r -s -p "新管理密码（12-128 位）: " value
      echo
      if [[ -n "${value}" ]]; then
        read -r -s -p "再次输入密码: " repeated
        echo
        if [[ "${value}" != "${repeated}" ]]; then
          echo "两次密码不一致，未修改。"
          pause
          return
        fi
      fi
      ;;
    listen_port)
      echo "请先放行新 TCP 端口；HTTPS 部署将修改 Nginx 端口，TCP 80 仍用于证书续期。"
      read -r -p "新网页端口（1-65535）: " value
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
  echo "请先在安全组/防火墙放行新 SSH 端口，并保持当前连接。"
  echo "旧端口将停止接受新连接；修改后需在另一个终端验证登录。"
  read -r -p "新 SSH 端口（1-65535，留空取消）: " port
  if [[ -n "${port}" ]]; then
    read -r -p "已放行新端口并准备验证？输入 yes 继续: " confirmed
    if [[ "${confirmed}" == "yes" ]]; then
      printf '%s' "${port}" | python3 /opt/s2l/manage_ssh.py || result=$?
    else
      echo "已取消"
    fi
  fi
  pause
  return "${result}"
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
    3) service_action restart "s2l 已重启" ;;
    4) show_logs ;;
    5) service_action start "s2l 已启动" ;;
    6) service_action stop "s2l 已停止" ;;
    7) change_setting admin_pass ;;
    8) change_setting admin_user ;;
    9) change_setting listen_port ;;
    10) change_ssh_port ;;
    0) exit 0 ;;
    *) echo "无效选项"; pause ;;
  esac
}

if (( DIRECT )); then
  dispatch "$1"
  exit $?
fi

while true; do
  clear || true
  echo "s2l 管理菜单"
  echo "────────────────────"
  echo "1) 查看网页管理地址"
  echo "2) 查看服务状态"
  echo "3) 重启服务"
  echo "4) 查看最近日志"
  echo "5) 启动服务"
  echo "6) 停止服务"
  echo "7) 修改管理密码"
  echo "8) 修改管理用户名"
  echo "9) 修改网页管理端口"
  echo "10) 修改 SSH 端口"
  echo "0) 退出"
  echo
  read -r -p "请选择操作 [0-10]: " choice
  dispatch "${choice}" || true
done

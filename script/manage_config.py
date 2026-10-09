#!/usr/bin/env python3
"""Update a management setting, restarting s2l with rollback on failure."""

import json
import errno
import os
from pathlib import Path
import re
import secrets
import socket
import ssl
import subprocess
import sys
import tempfile
import time
from urllib.parse import urlsplit, urlunsplit


CONFIG_FILE = Path("/opt/s2l/config.json")
NGINX_SITE = Path("/etc/nginx/conf.d/s2l.conf")


def validate_port(value):
    if not re.fullmatch(r"[0-9]{1,5}", value) or not 1 <= int(value) <= 65535:
        raise ValueError("端口需为 1-65535 的整数")
    return int(value)


def check_available_port(port):
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("0.0.0.0", port))
    if socket.has_ipv6:
        try:
            listener = socket.socket(socket.AF_INET6, socket.SOCK_STREAM)
        except OSError:
            return
        with listener:
            listener.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
            try:
                listener.bind(("::", port))
            except OSError as error:
                if error.errno not in (errno.EAFNOSUPPORT, errno.EADDRNOTAVAIL, errno.ENODEV):
                    raise


def random_port():
    for _ in range(128):
        port = 10000 + secrets.randbelow(50001)
        try:
            check_available_port(port)
            return port
        except OSError:
            continue
    raise RuntimeError("无法找到空闲端口")


def update_config(config, field, value):
    if not isinstance(config, dict):
        raise ValueError("配置必须是 JSON 对象")
    if field == "admin_user":
        if not re.fullmatch(r"[A-Za-z0-9_.-]{1,64}", value):
            raise ValueError("用户名需为 1-64 位字母、数字、点、下划线或连字符")
        config[field] = value
    elif field == "admin_pass":
        if not re.fullmatch(r"[A-Za-z0-9_.@+-]{12,128}", value):
            raise ValueError("密码需为 12-128 位字母、数字或 _ . @ + -")
        config[field] = value
    elif field == "listen_port":
        port = validate_port(value)
        if str(config.get("public_url", "")).startswith("https://"):
            if port == 80:
                raise ValueError("TCP 80 用于证书验证，不能作为 HTTPS 端口")
            address = urlsplit(config["public_url"])
            if not address.hostname or address.username or address.password:
                raise ValueError("HTTPS 管理地址格式错误")
            host = address.hostname
            if ":" in host:
                host = f"[{host}]"
            config["public_url"] = urlunsplit(("https", f"{host}:{port}", "", "", ""))
            return config
        listen = config.get("listen", "127.0.0.1:8080")
        host, separator, _ = listen.rpartition(":")
        if not separator:
            raise ValueError("原监听地址格式错误，请检查配置")
        config["listen"] = f"{host}:{port}"
    else:
        raise ValueError("不支持的配置项")
    user, password = config.get("admin_user", ""), config.get("admin_pass", "")
    if not user or len(password) < 12:
        raise ValueError("请先在配置文件中设置管理用户名和至少 12 位的密码")
    return config


def write_atomic(path, data, mode=0o600):
    fd, temporary = tempfile.mkstemp(prefix=".s2l-config-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as output:
            os.fchmod(output.fileno(), mode)
            output.write(data)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def restart_service():
    subprocess.run(["systemctl", "restart", "s2l"], check=True)
    time.sleep(1)
    subprocess.run(["systemctl", "is-active", "--quiet", "s2l"], check=True)


def change_https_port(original, value):
    current = json.loads(original)
    config = update_config(dict(current), "listen_port", value)
    port = validate_port(value)
    address = urlsplit(current["public_url"])
    old_port = address.port or 443
    if port == old_port:
        if config != current:
            write_atomic(CONFIG_FILE, (json.dumps(config, ensure_ascii=False, indent=2) + "\n").encode())
        return config["public_url"]
    if port == int(current["listen"].rsplit(":", 1)[1]):
        raise ValueError("HTTPS 端口不能与本机后端端口相同")
    check_available_port(port)
    if NGINX_SITE.is_symlink():
        raise ValueError("不支持符号链接形式的 Nginx 配置")
    site_original = NGINX_SITE.read_bytes()
    site = site_original.decode()
    if not site.startswith("# Managed by s2l HTTPS\n"):
        raise ValueError("仅支持 s2l 管理的 Nginx HTTPS 配置")
    site, listeners = re.subn(r"(?m)^(\s*listen\s+)" + str(old_port) + r"(\s+ssl\s*;)", rf"\g<1>{port}\g<2>", site)
    site, redirects = re.subn(r"(?m)^(\s*location / \{ return 308 )https://[^\s;$]+\$request_uri; \}", lambda m: m[1] + config["public_url"] + "$request_uri; }", site)
    if listeners != 1 or redirects != 1:
        raise ValueError("Nginx 配置与管理地址不一致，未修改")
    try:
        write_atomic(NGINX_SITE, site.encode(), 0o644)
        subprocess.run(["nginx", "-t"], check=True, capture_output=True)
        write_atomic(CONFIG_FILE, (json.dumps(config, ensure_ascii=False, indent=2) + "\n").encode())
        subprocess.run(["systemctl", "reload", "nginx"], check=True, capture_output=True)
        # Validate the local TLS listener; browser certificate trust is unchanged.
        context = ssl._create_unverified_context()
        for attempt in range(20):
            try:
                with socket.create_connection(("127.0.0.1", port), timeout=2) as connection:
                    with context.wrap_socket(connection, server_hostname=address.hostname):
                        return config["public_url"]
            except OSError:
                if attempt == 19:
                    raise RuntimeError("新 HTTPS 端口未正常监听")
                time.sleep(0.25)
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        write_atomic(CONFIG_FILE, original)
        write_atomic(NGINX_SITE, site_original, 0o644)
        try:
            subprocess.run(["nginx", "-t"], check=True, capture_output=True)
            subprocess.run(["systemctl", "reload", "nginx"], check=True, capture_output=True)
        except (OSError, subprocess.CalledProcessError):
            raise RuntimeError("已恢复原配置，但 Nginx 重载失败，请检查服务日志") from error
        raise RuntimeError("HTTPS 端口修改失败，已恢复原配置") from error


def main():
    if len(sys.argv) != 2:
        raise ValueError("缺少配置项")
    if sys.argv[1] == "random_port":
        print(random_port())
        return
    original = CONFIG_FILE.read_bytes()
    value = sys.stdin.read()
    if sys.argv[1] == "listen_port" and str(json.loads(original).get("public_url", "")).startswith("https://"):
        print(change_https_port(original, value))
        return
    config = update_config(json.loads(original), sys.argv[1], value)
    if sys.argv[1] == "listen_port":
        if config == json.loads(original):
            print("网页端口未改变")
            return
        check_available_port(validate_port(config["listen"].rsplit(":", 1)[1]))
    write_atomic(CONFIG_FILE, (json.dumps(config, ensure_ascii=False, indent=2) + "\n").encode())
    try:
        restart_service()
    except (OSError, subprocess.CalledProcessError) as error:
        write_atomic(CONFIG_FILE, original)
        try:
            restart_service()
        except (OSError, subprocess.CalledProcessError):
            raise RuntimeError("已恢复原配置，但服务启动失败；请查看 journalctl -u s2l") from error
        raise RuntimeError("新配置启动失败，已恢复原配置和服务") from error
    print("设置已保存，s2l 已重启")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"修改失败：{error}", file=sys.stderr)
        sys.exit(1)

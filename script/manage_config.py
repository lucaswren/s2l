#!/usr/bin/env python3
"""Update a management setting, restarting s2l with rollback on failure."""

import json
import errno
import os
from pathlib import Path
import re
import secrets
import socket
import subprocess
import sys
import tempfile
import time


CONFIG_FILE = Path("/opt/s2l/config.json")


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
        if str(config.get("public_url", "")).startswith("https://"):
            raise ValueError("HTTPS 管理地址使用 443 端口；此处不修改反向代理的后端端口")
        port = validate_port(value)
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


def write_atomic(path, data):
    fd, temporary = tempfile.mkstemp(prefix=".s2l-config-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as output:
            os.fchmod(output.fileno(), 0o600)
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


def main():
    if len(sys.argv) != 2:
        raise ValueError("缺少配置项")
    if sys.argv[1] == "random_port":
        print(random_port())
        return
    original = CONFIG_FILE.read_bytes()
    config = update_config(json.loads(original), sys.argv[1], sys.stdin.read())
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

#!/usr/bin/env python3
"""Change the OpenSSH listening port with configuration validation and rollback."""

import base64
import glob
import json
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import time

from manage_config import check_available_port, validate_port, write_atomic


SSH_CONFIG = Path("/etc/ssh/sshd_config")
SOCKET_CONFIG = Path("/etc/systemd/system/ssh.socket.d/99-s2l-port.conf")
BACKUP = Path("/opt/s2l/ssh-port-backup.json")
PORT_LINE = re.compile(r"^\s*Port(?:\s+|=)", re.IGNORECASE)
INCLUDE_LINE = re.compile(r"^\s*Include(?:\s+|=)\s*(.*)$", re.IGNORECASE)


def run(*args):
    result = subprocess.run(args, capture_output=True, text=True, check=True)
    return result.stdout


def active(unit):
    return subprocess.run(["systemctl", "is-active", "--quiet", unit]).returncode == 0


def config_files(path, visited=None):
    visited = visited if visited is not None else set()
    if path in visited:
        return {}
    if path.is_symlink() or not path.resolve().is_relative_to(SSH_CONFIG.parent):
        raise ValueError("SSH 配置包含链接或 /etc/ssh 外的文件，请手动修改")
    visited.add(path)
    original = path.read_bytes()
    files = {path: original}
    for line in original.decode().splitlines():
        match = INCLUDE_LINE.match(line)
        if match:
            for pattern in shlex.split(match.group(1), comments=True):
                candidate = Path(pattern)
                if not candidate.is_absolute():
                    candidate = SSH_CONFIG.parent / candidate
                for child in sorted(glob.glob(str(candidate))):
                    files.update(config_files(Path(child), visited))
    return files


def rewrite_ports(original, port, primary=False):
    lines = original.decode().splitlines(keepends=True)
    content = "".join("# s2l disabled: " + line if PORT_LINE.match(line) else line for line in lines)
    return ((f"Port {port}\n" if primary else "") + content).encode()


def effective_ports(output):
    return {int(line.split()[1]) for line in output.splitlines() if line.startswith("port ")}


def socket_override(output):
    addresses = [line.split()[1] for line in output.splitlines() if line.startswith("listenaddress ")]
    if not addresses:
        raise ValueError("未能读取 SSH 监听地址")
    return ("[Socket]\nListenStream=\n" + "".join(f"ListenStream={address}\n" for address in addresses)).encode()


def apply_runtime(service, socket_enabled):
    if socket_enabled:
        run("systemctl", "stop", service)
        run("systemctl", "daemon-reload")
        run("systemctl", "restart", "ssh.socket")
        run("systemctl", "start", service)
    else:
        run("systemctl", "reload", service)


def listening(port):
    for line in run("ss", "-H", "-ltn").splitlines():
        fields = line.split()
        if len(fields) > 3 and fields[3].rsplit(":", 1)[-1] == str(port):
            return True
    return False


def change_port(value):
    port = validate_port(value)
    sshd = shutil.which("sshd") or "/usr/sbin/sshd"
    run(sshd, "-t")
    previous = run(sshd, "-T")
    if effective_ports(previous) == {port} and listening(port):
        print("SSH 端口未改变")
        return
    check_available_port(port)
    socket_enabled = active("ssh.socket")
    service = next((unit for unit in ("ssh.service", "sshd.service") if active(unit)), None)
    if service is None:
        raise ValueError("SSH 服务未运行，请先启动 OpenSSH 服务")
    if socket_enabled and run("systemctl", "show", service, "-p", "KillMode", "--value").strip() != "process":
        raise ValueError("SSH 服务 KillMode 不是 process，自动修改可能中断当前连接，请手动修改")
    originals = config_files(SSH_CONFIG)
    if socket_enabled:
        if SOCKET_CONFIG.is_symlink():
            raise ValueError("SSH socket 配置为链接，请手动修改")
        originals[SOCKET_CONFIG] = SOCKET_CONFIG.read_bytes() if SOCKET_CONFIG.exists() else None
    backup = {str(path): base64.b64encode(data).decode() if data is not None else None for path, data in originals.items()}
    write_atomic(BACKUP, (json.dumps(backup, indent=2) + "\n").encode())
    runtime_changed = False
    try:
        for path, data in originals.items():
            if path != SOCKET_CONFIG:
                write_atomic(path, rewrite_ports(data, port, path == SSH_CONFIG))
        run(sshd, "-t")
        effective = run(sshd, "-T")
        if effective_ports(effective) != {port}:
            raise ValueError("SSH 有其他端口配置，自动修改已取消")
        addresses = [line.split()[1] for line in effective.splitlines() if line.startswith("listenaddress ")]
        if any(address.rsplit(":", 1)[-1] != str(port) for address in addresses):
            raise ValueError("ListenAddress 显式指定了其他端口，请手动修改")
        if socket_enabled:
            SOCKET_CONFIG.parent.mkdir(parents=True, exist_ok=True)
            write_atomic(SOCKET_CONFIG, socket_override(effective))
        runtime_changed = True
        apply_runtime(service, socket_enabled)
        for _ in range(25):
            if listening(port) and active(service) and (not socket_enabled or active("ssh.socket")):
                print(f"SSH 端口已改为 {port}；请在新终端使用 ssh -p {port} 用户名@服务器IP 验证登录")
                print(f"配置备份：{BACKUP}；确认新连接成功前不要关闭当前终端")
                return
            time.sleep(0.2)
        raise RuntimeError("新 SSH 端口未正常监听")
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        for path, data in originals.items():
            if data is None:
                path.unlink(missing_ok=True)
            else:
                write_atomic(path, data)
        if runtime_changed:
            try:
                apply_runtime(service, socket_enabled)
            except (OSError, subprocess.CalledProcessError):
                raise RuntimeError(f"原配置已恢复，但 SSH 重载失败；请通过服务器控制台检查，备份：{BACKUP}") from error
        raise RuntimeError("修改失败，已恢复原 SSH 配置") from error


if __name__ == "__main__":
    try:
        change_port(sys.stdin.read())
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"SSH 端口修改失败：{error}", file=sys.stderr)
        sys.exit(1)

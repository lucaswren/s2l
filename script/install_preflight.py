#!/usr/bin/env python3
"""Read-only host checks plus a disposable TUN capability probe."""
import ipaddress
import json
import os
from pathlib import Path
import re
import socket
import struct
import subprocess
import sys


def command(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=15).stdout


def go_version(text):
    match = re.search(r'go(\d+)\.(\d+)(?:\.(\d+))?', text)
    if not match or tuple(map(int, match.groups(default='0'))) < (1, 22, 0):
        raise ValueError('Go 需要 1.22 或更新版本；请安装兼容工具链后重试')


def singbox_version(text):
    if not re.search(r'^sing-box version 1\.11\.15\s*$', text, re.M):
        raise ValueError('仅支持已验证的 sing-box 1.11.15；已有程序不会被自动覆盖')


def kernel_version(distro, release):
    # Ubuntu bug 2169477: PPPoL2TP session refcount regression. The VM's
    # 6.8.0-147 kernel locked up in l2tp_tunnel_get_session after redial.
    if distro == 'ubuntu' and re.fullmatch(r'6\.8\.0-(146|147)-generic', release):
        raise RuntimeError('当前 Ubuntu 内核存在已知 L2TP 崩溃问题（2169477）；请切换已修复内核或 HWE 内核并重启后重试')


def system():
    if sys.platform != 'linux' or os.geteuid() != 0:
        raise RuntimeError('requires Linux root')
    release = dict(line.split('=', 1) for line in Path('/etc/os-release').read_text().splitlines() if '=' in line)
    distro = release.get('ID', '').strip('"')
    kernel_version(distro, os.uname().release)
    if distro not in ('ubuntu', 'debian'):
        raise RuntimeError('installer supports Ubuntu and Debian only')
    if not Path('/run/systemd/system').is_dir() or command('systemctl', 'show', '--property=Version').strip() == '':
        raise RuntimeError('systemd must be running as the system service manager')
    for name in ('/opt/s2l', '/etc/ppp', '/etc/xl2tpd', '/usr/local/bin'):
        if Path(name).is_symlink():
            raise RuntimeError('installation target must not be a symlink: ' + name)
    if Path('/opt/s2l/config.json').exists():
        raise RuntimeError('S2L is already installed; use the upgrade command')
    if Path('/opt/s2l').exists() and any(Path('/opt/s2l').iterdir()):
        raise RuntimeError('/opt/s2l contains existing files; refusing to overwrite')
    if Path('/usr/local/bin/s2l').exists() or Path('/usr/local/bin/s2l').is_symlink():
        raise RuntimeError('/usr/local/bin/s2l is already in use')
    chap = Path('/etc/ppp/chap-secrets')
    if chap.exists() and any(line.strip() and not line.lstrip().startswith('#') for line in chap.read_text().splitlines()):
        raise RuntimeError('existing PPP accounts detected; use a dedicated host')
    if subprocess.run(['pgrep', '-x', 'pppd'], capture_output=True).returncode == 0:
        raise RuntimeError('existing PPP sessions detected; use a dedicated host')
    for unit in ('xl2tpd', 's2l'):
        if subprocess.run(['systemctl', 'is-active', '--quiet', unit], capture_output=True).returncode == 0:
            raise RuntimeError('existing tunnel service is active: ' + unit)


def capabilities():
    import fcntl
    for module in ('tun', 'ppp_generic', 'l2tp_ppp'):
        command('modprobe', module)
    fd = os.open('/dev/net/tun', os.O_RDWR)
    try:
        fcntl.ioctl(fd, 0x400454ca, struct.pack('16sH', b's2lcheck%d', 0x1001))
    finally:
        os.close(fd)  # Nonpersistent probe disappears immediately.
    fd = os.open('/dev/ppp', os.O_RDWR)
    os.close(fd)
    interfaces = json.loads(command('ip', '-j', 'address', 'show'))
    if any(row.get('ifname', '').startswith('ppp') for row in interfaces):
        raise RuntimeError('existing PPP interfaces detected; use a dedicated host')
    for binary in ('iptables', 'ip6tables'):
        command(binary, '-w', '5', '-S')
    for chain in ('S2L_FORWARD', 'S2L_MANAGEMENT'):
        if subprocess.run(['iptables', '-w', '5', '-S', chain], capture_output=True).returncode == 0:
            raise RuntimeError('existing S2L firewall state detected; recover previous installation first')
    for family, kind, port in ((socket.AF_INET, socket.SOCK_STREAM, 8080), (socket.AF_INET, socket.SOCK_DGRAM, 1701)):
        with socket.socket(family, kind) as probe:
            probe.bind(('0.0.0.0', port))


if __name__ == '__main__':
    try:
        action = sys.argv[1]
        if action == 'system':
            system()
        elif action == 'capabilities':
            capabilities()
        elif action == 'go':
            go_version(command(sys.argv[2], 'version'))
        elif action == 'singbox':
            singbox_version(command(sys.argv[2], 'version'))
        else:
            raise ValueError('unknown preflight check')
    except Exception as error:
        raise SystemExit(str(error))

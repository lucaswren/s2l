#!/usr/bin/env python3
"""Manage the dedicated s2l L2TP/IPsec service without exposing its PSK."""

import base64
import fcntl
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time

from manage_config import write_atomic

ROOT = Path('/opt/s2l')
STATE = ROOT / 'ipsec-settings.json'
CONF = ROOT / 'ipsec.conf'
SECRET = ROOT / 'ipsec.secrets'
SYSTEM_SECRET = Path('/etc/ipsec.secrets')
UNIT = 's2l-ipsec.service'
CHAIN = 'S2L-IPSEC'
INCLUDE = 'include /opt/s2l/ipsec.secrets'
FILES = (STATE, CONF, SECRET, SYSTEM_SECRET)


def run(*args, check=True):
    result = subprocess.run(args, capture_output=True, text=True, timeout=20)
    if check and result.returncode:
        # Never print command output: configuration diagnostics may contain secrets.
        raise RuntimeError(f'{args[0]} 操作失败（退出码 {result.returncode}）')
    return result


def active(unit):
    return run('systemctl', 'is-active', '--quiet', unit, check=False).returncode == 0


def read_state():
    return json.loads(STATE.read_text()) if STATE.exists() else {'enabled': False, 'psk': ''}


def ready():
    return (Path('/usr/lib/ipsec/starter').is_file()
            and Path('/etc/systemd/system/s2l-ipsec.service').is_file()
            and shutil.which('iptables') is not None)


def firewall(enabled):
    def ipt(*args, check=True):
        return run('iptables', '-w', '5', *args, check=check)
    exists = ipt('-S', CHAIN, check=False).returncode == 0
    linked = ipt('-C', 'INPUT', '-j', CHAIN, check=False).returncode == 0
    if not enabled:
        if linked:
            ipt('-D', 'INPUT', '-j', CHAIN)
        if exists:
            ipt('-F', CHAIN)
            ipt('-X', CHAIN)
        return
    if not exists:
        ipt('-N', CHAIN)
        try:
            ipt('-A', CHAIN, '-p', 'udp', '-m', 'multiport', '--dports', '500,4500', '-j', 'ACCEPT')
            ipt('-A', CHAIN, '-p', 'esp', '-j', 'ACCEPT')
            ipt('-A', CHAIN, '-p', 'udp', '--dport', '1701', '-m', 'policy', '--dir', 'in', '--pol', 'ipsec', '-j', 'ACCEPT')
            ipt('-A', CHAIN, '-p', 'udp', '--dport', '1701', '-j', 'DROP')
        except Exception:
            ipt('-F', CHAIN, check=False)
            ipt('-X', CHAIN, check=False)
            raise
    if not linked:
        ipt('-I', 'INPUT', '1', '-j', CHAIN)


def status():
    state = read_state()
    protected = run('iptables', '-w', '5', '-C', 'INPUT', '-j', CHAIN, check=False).returncode == 0 if shutil.which('iptables') else False
    return {'available': ready(), 'enabled': bool(state.get('enabled')),
            'active': active(UNIT), 'psk_configured': bool(state.get('psk')),
            'protected': protected}


def check_conflicts():
    for unit in ('strongswan-starter.service', 'strongswan.service', 'charon-systemd.service'):
        if active(unit):
            raise ValueError('检测到其他 IPsec 服务正在运行，请先处理现有 VPN 配置')
    # A wildcard PSK would also affect unrelated IPsec connections.
    if SYSTEM_SECRET.exists():
        for line in SYSTEM_SECRET.read_text().splitlines():
            text = line.strip()
            if text and not text.startswith('#') and text != INCLUDE:
                raise ValueError('/etc/ipsec.secrets 包含其他配置，请先处理现有 VPN 配置')
    for line in run('ss', '-H', '-lun').stdout.splitlines():
        fields = line.split()
        if len(fields) > 3 and fields[3].rsplit(':', 1)[-1] in ('500', '4500') and not active(UNIT):
            raise ValueError('UDP 500 或 4500 已被其他服务占用')


def render_conf():
    return b'''# Managed by s2l
config setup
    uniqueids=no
conn s2l-l2tp
    keyexchange=ikev1
    authby=secret
    type=transport
    left=%defaultroute
    leftprotoport=17/1701
    right=%any
    rightprotoport=17/%any
    ike=aes256-sha256-modp2048,aes256-sha1-modp2048,aes128-sha1-modp2048,aes256-sha1-modp1024,aes128-sha1-modp1024!
    esp=aes256-sha256,aes256-sha1,aes128-sha1!
    dpdaction=clear
    dpddelay=30s
    dpdtimeout=120s
    rekey=no
    auto=add
'''


def change(request):
    if not isinstance(request.get('enabled'), bool):
        raise ValueError('enabled 必须为布尔值')
    if not ready():
        raise ValueError('IPsec 依赖未安装，请升级服务器安装文件')
    old = read_state()
    enabled = request['enabled']
    psk = request.get('psk', '') or old.get('psk', '')
    if not isinstance(psk, str) or (psk and not re.fullmatch(r'[A-Za-z0-9_.@+-]{12,128}', psk)):
        raise ValueError('预共享密钥需为 12–128 位字母、数字或 _ . @ + -')
    if enabled and not psk:
        raise ValueError('启用 IPsec 时必须设置预共享密钥')
    check_conflicts()
    originals = {path: path.read_bytes() if path.exists() else None for path in FILES}
    was_active = active(UNIT)
    was_enabled = run('systemctl', 'is-enabled', '--quiet', UNIT, check=False).returncode == 0
    backup = {str(path): base64.b64encode(data).decode() if data is not None else None for path, data in originals.items()}
    write_atomic(ROOT / 'ipsec-backup.json', (json.dumps(backup, indent=2) + '\n').encode())
    try:
        write_atomic(CONF, render_conf())
        write_atomic(SECRET, (': PSK "' + psk + '"\n').encode() if psk else b'')
        content = SYSTEM_SECRET.read_text() if SYSTEM_SECRET.exists() else ''
        if INCLUDE not in content.splitlines():
            write_atomic(SYSTEM_SECRET, (content.rstrip() + '\n' + INCLUDE + '\n').encode())
        write_atomic(STATE, (json.dumps({'enabled': enabled, 'psk': psk}) + '\n').encode())
        if enabled:
            firewall(True)
            run('systemctl', 'enable', UNIT)
            run('systemctl', 'restart', UNIT)
            for _ in range(10):
                time.sleep(0.5)
                if active(UNIT) and 's2l-l2tp:' in run('ipsec', 'statusall', check=False).stdout:
                    break
            else:
                raise RuntimeError('IPsec 服务启动失败或连接配置未加载')
        else:
            run('systemctl', 'disable', '--now', UNIT)
            firewall(False)
    except Exception as error:
        for path, data in originals.items():
            if data is None:
                path.unlink(missing_ok=True)
            else:
                write_atomic(path, data)
        try:
            run('systemctl', 'enable' if was_enabled else 'disable', UNIT)
            if was_active:
                firewall(True)
                run('systemctl', 'restart', UNIT)
            else:
                run('systemctl', 'stop', UNIT)
                firewall(bool(old.get('enabled')))
        except Exception:
            raise RuntimeError('修改失败，原文件已恢复，但服务或防火墙恢复失败；请通过 SSH 检查') from error
        raise RuntimeError('修改失败，已恢复原配置、服务和防火墙') from error
    return {'message': 'L2TP/IPsec 已启用' if enabled else 'IPsec 已关闭，恢复普通 L2TP', **status()}


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else ''
    if mode == 'status':
        print(json.dumps(status()))
    elif mode == 'firewall':
        firewall(bool(read_state().get('enabled')))
    elif mode == 'apply':
        with open(ROOT / 'ipsec.lock', 'a') as lock:
            os.chmod(lock.name, 0o600)
            fcntl.flock(lock, fcntl.LOCK_EX)
            print(json.dumps(change(json.load(sys.stdin))))
    else:
        raise ValueError('不支持的操作')


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)

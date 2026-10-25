#!/usr/bin/env python3
"""Manage the dedicated S2L L2TP/IPsec service without exposing its PSK."""

import base64
import fcntl
import json
import os
from pathlib import Path
import re
import shlex
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
OUT_CHAIN = 'S2L-IPSEC-OUT'
PEER_CHAIN = 'S2L-IPSEC-PEERS'
INCLUDE = 'include /opt/s2l/ipsec.secrets'
FILES = (STATE, CONF, SECRET, SYSTEM_SECRET)
APPARMOR_PROFILE = Path('/etc/apparmor.d/usr.lib.ipsec.charon')
APPARMOR_LOCAL = Path('/etc/apparmor.d/local/usr.lib.ipsec.charon')
APPARMOR_ENABLED = Path('/sys/module/apparmor/parameters/enabled')
APPARMOR_SECRET_RULE = '"/opt/s2l/ipsec.secrets" r, # s2l-managed-secret'


def run(*args, check=True, input=None):
    result = subprocess.run(args, input=input, capture_output=True, text=True, timeout=20)
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
            and shutil.which('iptables') is not None
            and shutil.which('iptables-restore') is not None
            and shutil.which('conntrack') is not None
            and (ROOT / 'ipsec_nat.py').is_file())


def prepare_apparmor():
    """Allow only the managed PSK file, preserving the packaged confinement."""
    if not APPARMOR_PROFILE.is_file():
        return
    if not re.search(r'#include\s+<local/usr\.lib\.ipsec\.charon>', APPARMOR_PROFILE.read_text()):
        raise RuntimeError('charon AppArmor profile lacks its local include; refusing startup')
    enabled = APPARMOR_ENABLED.exists() and APPARMOR_ENABLED.read_text().strip().lower() == 'y'
    parser = shutil.which('apparmor_parser') if enabled else None
    if enabled and not parser:
        raise RuntimeError('AppArmor is enabled but apparmor_parser is missing; refusing startup')
    if APPARMOR_LOCAL.is_symlink():
        raise RuntimeError('charon AppArmor local include is a symlink; refusing replacement')
    original = APPARMOR_LOCAL.read_text() if APPARMOR_LOCAL.exists() else ''
    if APPARMOR_SECRET_RULE not in original.splitlines():
        APPARMOR_LOCAL.parent.mkdir(parents=True, exist_ok=True)
        write_atomic(APPARMOR_LOCAL, (original.rstrip('\n') + '\n' + APPARMOR_SECRET_RULE + '\n').encode())
    if enabled:
        # Also retry reload when a previous attempt wrote the include but failed
        # to load it. Never disable AppArmor or replace administrator rules.
        run(parser, '-r', str(APPARMOR_PROFILE))


def firewall_rules():
    return [
        ['-p', 'udp', '-m', 'multiport', '--dports', '500,4500', '-j', 'ACCEPT'],
        ['-p', 'esp', '-j', 'ACCEPT'],
        ['-p', 'udp', '-m', 'udp', '--dport', '1701', '-m', 'policy', '--dir', 'in', '--pol', 'ipsec', '-j', PEER_CHAIN],
        ['-p', 'udp', '-m', 'udp', '--dport', '1701', '-j', 'DROP'],
    ]


def outgoing_rules():
    return [['-p', 'udp', '-m', 'udp', '--sport', '1701', '-m', 'policy',
             '--dir', 'out', '--pol', 'none', '-j', 'DROP']]


def firewall_protected():
    if not shutil.which('iptables'):
        return False
    chain = run('iptables', '-w', '5', '-S', CHAIN, check=False)
    incoming = run('iptables', '-w', '5', '-S', 'INPUT', check=False)
    if chain.returncode or incoming.returncode:
        return False
    if run('iptables', '-w', '5', '-S', PEER_CHAIN, check=False).returncode:
        return False
    rules = [shlex.split(line) for line in chain.stdout.splitlines() if line.startswith('-A ')]
    jumps = [shlex.split(line) for line in incoming.stdout.splitlines() if line.startswith('-A ')]
    if rules != [['-A', CHAIN, *rule] for rule in firewall_rules()]:
        return False
    outgoing = run('iptables', '-w', '5', '-S', 'OUTPUT', check=False)
    guard = run('iptables', '-w', '5', '-S', OUT_CHAIN, check=False)
    out_jumps = [shlex.split(line) for line in outgoing.stdout.splitlines() if line.startswith('-A ')]
    out_rules = [shlex.split(line) for line in guard.stdout.splitlines() if line.startswith('-A ')]
    if (outgoing.returncode or guard.returncode
            or out_rules != [['-A', OUT_CHAIN, *rule] for rule in outgoing_rules()]
            or not out_jumps or out_jumps[0] != ['-A', 'OUTPUT', '-j', OUT_CHAIN]):
        return False
    for rule in jumps:
        if rule == ['-A', 'INPUT', '-j', CHAIN]:
            return True
        if rule == ['-A', 'INPUT', '-i', 'ppp+', '-j', 'DROP']:
            continue
        if rule == ['-A', 'INPUT', '-i', 'ppp+', '-j', 'S2L_MANAGEMENT']:
            # Mapping startup can prepend this owned guard. It accepts only
            # configured TCP management traffic, then drops everything else.
            # Verify that property rather than fighting its INPUT position.
            management = run('iptables', '-w', '5', '-S', 'S2L_MANAGEMENT', check=False)
            rows = [shlex.split(line) for line in management.stdout.splitlines() if line.startswith('-A ')]
            if (management.returncode == 0 and rows
                    and rows[-1] == ['-A', 'S2L_MANAGEMENT', '-j', 'DROP']
                    and all(row[:2] == ['-A', 'S2L_MANAGEMENT'] and row[-2:] == ['-j', 'ACCEPT']
                            and '!' not in row and any(row[i:i+2] == ['-p', 'tcp'] for i in range(len(row)))
                            for row in rows[:-1])):
                continue
        # An unknown preceding rule might accept unprotected UDP/1701.
        return False
    return False


def firewall(enabled):
    def ipt(*args, check=True):
        return run('iptables', '-w', '5', *args, check=check)
    exists = ipt('-S', CHAIN, check=False).returncode == 0
    linked = ipt('-C', 'INPUT', '-j', CHAIN, check=False).returncode == 0
    if not enabled:
        while linked:
            ipt('-D', 'INPUT', '-j', CHAIN)
            linked = ipt('-C', 'INPUT', '-j', CHAIN, check=False).returncode == 0
        if exists:
            ipt('-F', CHAIN)
            ipt('-X', CHAIN)
        if ipt('-S', PEER_CHAIN, check=False).returncode == 0:
            ipt('-F', PEER_CHAIN)
            ipt('-X', PEER_CHAIN)
        while ipt('-C', 'OUTPUT', '-j', OUT_CHAIN, check=False).returncode == 0:
            ipt('-D', 'OUTPUT', '-j', OUT_CHAIN)
        if ipt('-S', OUT_CHAIN, check=False).returncode == 0:
            ipt('-F', OUT_CHAIN)
            ipt('-X', OUT_CHAIN)
        return
    if firewall_protected():
        return
    # Repair missing/reordered rules and jumps as one table transaction. Never
    # expose a temporarily empty, already linked chain by flushing it in place.
    incoming = ipt('-S', 'INPUT').stdout.splitlines()
    # Do not redeclare/flush the dynamic chain during repair of static guards.
    payload = ['*filter']
    if ipt('-S', PEER_CHAIN, check=False).returncode:
        payload.append(f':{PEER_CHAIN} - [0:0]')
    payload += [f':{CHAIN} - [0:0]', f'-F {CHAIN}']
    payload += [shlex.join(['-A', CHAIN, *rule]) for rule in firewall_rules()]
    payload += [f'-D INPUT -j {CHAIN}' for line in incoming
                if shlex.split(line) == ['-A', 'INPUT', '-j', CHAIN]]
    outgoing = ipt('-S', 'OUTPUT').stdout.splitlines()
    payload += [f'-I INPUT 1 -j {CHAIN}', f':{OUT_CHAIN} - [0:0]', f'-F {OUT_CHAIN}']
    payload += [shlex.join(['-A', OUT_CHAIN, *rule]) for rule in outgoing_rules()]
    payload += [f'-D OUTPUT -j {OUT_CHAIN}' for line in outgoing
                if shlex.split(line) == ['-A', 'OUTPUT', '-j', OUT_CHAIN]]
    payload += [f'-I OUTPUT 1 -j {OUT_CHAIN}', 'COMMIT', '']
    run('iptables-restore', '--wait', '5', '--noflush', input='\n'.join(payload))


def status():
    state = read_state()
    protected = firewall_protected()
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
    hook = shlex.quote(str(Path(__file__).resolve().with_name('ipsec_nat.py')))
    return ('''# Managed by s2l
config setup
    uniqueids=no
conn s2l-l2tp
    keyexchange=ikev1
    authby=secret
    type=transport
    forceencaps=yes
    mark=%unique
    leftupdown="/usr/bin/python3 ''' + hook + '''"
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
''').encode()


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
        enabled = bool(read_state().get('enabled'))
        firewall(enabled)
        if enabled:
            if not ready():
                raise RuntimeError('IPsec dependencies missing (conntrack/ipsec_nat.py); refusing startup')
            prepare_apparmor()
            # Upgrade/restart refreshes generated transport settings, preserving PSK.
            write_atomic(CONF, render_conf())
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

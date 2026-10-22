#!/usr/bin/env python3
"""Private first-install transaction. Never used for upgrades or arbitrary paths."""
import json
import os
from pathlib import Path
import secrets
import shutil
import stat
import subprocess
import sys

ROOT = Path('/var/lib/s2l-install')
APP = Path('/opt/s2l')
FILES = (
 '/usr/local/bin/s2l', '/etc/sysctl.d/99-s2l.conf',
 '/etc/systemd/system/s2l.service', '/etc/systemd/system/s2l-guard.service',
 '/etc/systemd/system/s2l-ipsec.service', '/etc/systemd/system/xl2tpd.service.d/s2l.conf',
 '/etc/ppp/chap-secrets', '/etc/ppp/options.xl2tpd', '/etc/xl2tpd/xl2tpd.conf',
 '/etc/nginx/conf.d/s2l.conf', '/etc/nginx/sites-enabled/default',
)
UNITS = ('s2l', 's2l-guard', 's2l-ipsec', 'xl2tpd', 'nginx')
KEYS = ('net.ipv4.ip_forward', 'net.ipv4.conf.all.rp_filter', 'net.ipv4.conf.default.rp_filter')


def run(*args, check=True):
    return subprocess.run(args, check=check, text=True, capture_output=True, timeout=15)


def secure_root():
    if ROOT.is_symlink():
        raise RuntimeError('installation journal must not be a symlink')
    ROOT.mkdir(mode=0o700, parents=True, exist_ok=True)
    info = ROOT.stat()
    if info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o700:
        raise RuntimeError('installation journal must be root-owned with mode 0700')


def save(value):
    temporary = ROOT / 'journal.tmp'
    with temporary.open('w') as out:
        os.fchmod(out.fileno(), 0o600)
        json.dump(value, out)
        out.flush()
        os.fsync(out.fileno())
    temporary.replace(ROOT / 'journal.json')


def begin():
    secure_root()
    if (ROOT / 'journal.json').exists():
        raise RuntimeError('recover the previous installation first')
    if APP.is_symlink() or (APP.exists() and any(APP.iterdir())):
        raise RuntimeError('/opt/s2l is not an empty installation directory')
    records = []
    for index, name in enumerate(FILES):
        path = Path(name)
        if path.exists() or path.is_symlink():
            if not path.is_file() and not path.is_symlink():
                raise RuntimeError('unexpected installation target: ' + name)
            if path.is_symlink():
                records.append({'kind': 'link', 'target': os.readlink(path)})
            else:
                shutil.copy2(path, ROOT / str(index))
                records.append({'kind': 'file'})
        else:
            records.append({'kind': 'absent'})
    units = {name: {'active': run('systemctl', 'is-active', '--quiet', name, check=False).returncode == 0,
                    'enabled': run('systemctl', 'is-enabled', '--quiet', name, check=False).returncode == 0}
             for name in UNITS}
    values = {key: run('sysctl', '-n', key).stdout.strip() for key in KEYS}
    if any(value not in ('0', '1', '2') for value in values.values()):
        raise RuntimeError('cannot snapshot network sysctl settings')
    token = secrets.token_hex(24)
    save({'token': token, 'files': records, 'units': units, 'sysctl': values, 'complete': False, 'ntp': run('timedatectl', 'show', '--property=NTP', '--value', check=False).stdout.strip()})
    APP.mkdir(mode=0o700, exist_ok=True)
    (APP / '.install-owner').write_text(token)


def recover():
    secure_root()
    journal = ROOT / 'journal.json'
    if not journal.exists():
        return
    state = json.loads(journal.read_text())
    if state.get('complete'):
        clear()
        return
    if len(state.get('files', [])) != len(FILES) or set(state.get('units', {})) != set(UNITS) or set(state.get('sysctl', {})) != set(KEYS):
        raise RuntimeError('invalid installation journal; refusing recovery')
    if APP.is_symlink():
        raise RuntimeError('installation directory was replaced; refusing recovery')
    if APP.exists():
        owner = APP / '.install-owner'
        if any(APP.iterdir()) and (not owner.is_file() or owner.is_symlink() or owner.read_text() != state['token']):
            raise RuntimeError('installation ownership changed; refusing recovery')
    for unit in ('s2l', 'xl2tpd', 's2l-guard', 's2l-ipsec'):
        run('systemctl', 'stop', unit, check=False)
    # Guards may be removed only after the tunnel daemon and its PPP children stop.
    if run('pgrep', '-x', 'pppd', check=False).returncode == 0:
        raise RuntimeError('PPP sessions still exist; leak guards retained for manual recovery')
    for binary, chain, target in (
        ('iptables', 'FORWARD', 'S2L_FORWARD'), ('iptables', 'INPUT', 'S2L_MANAGEMENT'),
        ('iptables', 'INPUT', 'DROP'), ('ip6tables', 'INPUT', 'DROP'), ('ip6tables', 'FORWARD', 'DROP'),
    ):
        for _ in range(8):
            if run(binary, '-w', '5', '-C', chain, '-i', 'ppp+', '-j', target, check=False).returncode:
                break
            run(binary, '-w', '5', '-D', chain, '-i', 'ppp+', '-j', target)
    for chain in ('S2L_FORWARD', 'S2L_MANAGEMENT'):
        if run('iptables', '-w', '5', '-S', chain, check=False).returncode == 0:
            run('iptables', '-w', '5', '-F', chain)
            run('iptables', '-w', '5', '-X', chain)
    for index, (name, record) in enumerate(zip(FILES, state['files'])):
        path = Path(name)
        if path.exists() or path.is_symlink():
            path.unlink()
        if record['kind'] == 'file':
            path.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / str(index), path)
        elif record['kind'] == 'link':
            path.parent.mkdir(parents=True, exist_ok=True)
            path.symlink_to(record['target'])
        elif record['kind'] != 'absent':
            raise RuntimeError('invalid snapshot kind')
    if APP.exists():
        shutil.rmtree(APP)  # Fixed absolute path, validated ownership above.
    for key, value in state['sysctl'].items():
        if value not in ('0', '1', '2'):
            raise RuntimeError('invalid sysctl snapshot')
        run('sysctl', '-w', key + '=' + value)
    if state.get('ntp') in ('yes', 'no'):
        run('timedatectl', 'set-ntp', 'true' if state['ntp'] == 'yes' else 'false')
    run('systemctl', 'daemon-reload')
    for name, previous in state['units'].items():
        run('systemctl', 'enable' if previous['enabled'] else 'disable', name, check=False)
        if previous['active']:
            run('systemctl', 'start', name)
        else:
            run('systemctl', 'stop', name, check=False)
    clear()
    print('安装未完成，已恢复安装前配置；依赖软件包保留，可重新执行安装。')


def clear():
    for path in ROOT.iterdir():
        if path.is_file() and not path.is_symlink():
            path.unlink()


def commit():
    secure_root()
    state = json.loads((ROOT / 'journal.json').read_text())
    state['complete'] = True
    save(state)
    (APP / '.install-owner').unlink(missing_ok=True)
    clear()


if __name__ == '__main__':
    if os.geteuid() != 0:
        raise SystemExit('requires root')
    try:
        {'begin': begin, 'recover': recover, 'commit': commit}[sys.argv[1]]()
    except Exception as error:
        raise SystemExit(str(error))

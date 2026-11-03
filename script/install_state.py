#!/usr/bin/env python3
"""Private first-install transaction. Never used for upgrades or arbitrary paths."""
from installer_i18n import tr
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import stat
import subprocess
import sys
import tempfile

ROOT = Path('/var/lib/s2l-install')
APP = Path('/opt/s2l')
FILES = (
 '/usr/local/bin/s2l', '/etc/sysctl.d/99-s2l.conf',
 '/etc/systemd/system/s2l.service', '/etc/systemd/system/s2l-guard.service',
 '/etc/systemd/system/s2l-ipsec.service', '/etc/systemd/system/xl2tpd.service.d/s2l.conf',
 '/etc/ppp/chap-secrets', '/etc/ppp/options.xl2tpd', '/etc/xl2tpd/xl2tpd.conf',
 '/etc/ppp/options.xl2tpd.s2l-up', '/etc/ppp/options.xl2tpd.s2l-down',
 '/etc/nginx/conf.d/s2l.conf', '/etc/nginx/sites-enabled/default',
)
UNITS = ('s2l', 's2l-guard', 's2l-ipsec', 'xl2tpd', 'nginx')
KEYS = ('net.ipv4.ip_forward', 'net.ipv4.conf.all.rp_filter', 'net.ipv4.conf.default.rp_filter')
APT_POLICY = Path('/etc/apt/apt.conf.d/99-s2l-manual-maintenance')
APT_POLICY_TEXT = (
    'APT::Periodic::Unattended-Upgrade "0";\n'
    'Unattended-Upgrade::InstallOnShutdown "false";\n'
    'Unattended-Upgrade::Automatic-Reboot "false";\n'
)


def matches_apt_policy():
    return (not APT_POLICY.is_symlink() and APT_POLICY.is_file()
            and APT_POLICY.read_bytes() == APT_POLICY_TEXT.encode())


def run(*args, check=True, timeout=15):
    return subprocess.run(args, check=check, text=True, capture_output=True, timeout=timeout)


def stop_and_verify(unit):
    run('systemctl', 'stop', unit, timeout=140, check=False)
    properties = run('systemctl', 'show', '--property=LoadState,ActiveState,MainPID,ControlPID', unit).stdout
    status = dict(line.split('=', 1) for line in properties.splitlines() if '=' in line)
    stopped = status.get('ActiveState') in ('inactive', 'failed')
    no_process = all(status.get(key) == '0' for key in ('MainPID', 'ControlPID'))
    if not stopped or not (no_process or status.get('LoadState') == 'not-found'):
        raise RuntimeError(f'{unit} still active or unverified; installation journal retained')


def secure_root():
    if ROOT.is_symlink():
        raise RuntimeError(tr('installation journal must not be a symlink'))
    ROOT.mkdir(mode=0o700, parents=True, exist_ok=True)
    info = ROOT.stat()
    if info.st_uid != 0 or stat.S_IMODE(info.st_mode) != 0o700:
        raise RuntimeError(tr('installation journal must be root-owned with mode 0700'))


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
        raise RuntimeError(tr('recover the previous installation first'))
    if APP.is_symlink() or (APP.exists() and any(APP.iterdir())):
        raise RuntimeError(tr('/opt/s2l is not an empty installation directory'))
    if 'table ip s2l_ppp_sessions' in run('nft', 'list', 'tables').stdout.splitlines():
        raise RuntimeError('existing S2L PPP session guard; inspect the previous installation first')
    for table in ('filter', 'mangle'):
        rules = run('iptables', '-w', '5', '-t', table, '-S').stdout.splitlines()
        if any(line in ('-N S2L_FORWARD', '-N S2L_RETURN', '-N S2L_MANAGEMENT')
               or 's2l:session-admission' in line or 's2l-tcpmss' in line for line in rules):
            raise RuntimeError('existing S2L firewall state; use a clean host or uninstall the previous installation')
    records = []
    for index, name in enumerate(FILES):
        path = Path(name)
        if path.exists() or path.is_symlink():
            if not path.is_file() and not path.is_symlink():
                raise RuntimeError(tr('unexpected installation target: ') + name)
            if path.is_symlink():
                records.append({'kind': 'link', 'target': os.readlink(path)})
            else:
                shutil.copy2(path, ROOT / str(index))
                records.append({'kind': 'file'})
        else:
            records.append({'kind': 'absent'})
    manual_apt = os.environ.get('S2L_APT_POLICY', 'manual') == 'manual'
    apt_preexisting = manual_apt and (APT_POLICY.exists() or APT_POLICY.is_symlink())
    if apt_preexisting and not matches_apt_policy():
        raise RuntimeError(tr('unexpected installation target: ') + str(APT_POLICY))
    units = {name: {'active': run('systemctl', 'is-active', '--quiet', name, check=False).returncode == 0,
                    'enabled': run('systemctl', 'is-enabled', '--quiet', name, check=False).returncode == 0}
             for name in UNITS}
    values = {key: run('sysctl', '-n', key).stdout.strip() for key in KEYS}
    if any(value not in ('0', '1', '2') for value in values.values()):
        raise RuntimeError(tr('cannot snapshot network sysctl settings'))
    token = secrets.token_hex(24)
    save({'token': token, 'files': records, 'units': units, 'sysctl': values, 'complete': False,
          'manual_apt': manual_apt, 'apt_preexisting': apt_preexisting, 'session_guard': True,
          'ntp': run('timedatectl', 'show', '--property=NTP', '--value', check=False).stdout.strip()})
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
        raise RuntimeError(tr('invalid installation journal; refusing recovery'))
    if APP.is_symlink():
        raise RuntimeError(tr('installation directory was replaced; refusing recovery'))
    if APP.exists():
        owner = APP / '.install-owner'
        if any(APP.iterdir()) and (not owner.is_file() or owner.is_symlink() or owner.read_text() != state['token']):
            raise RuntimeError(tr('installation ownership changed; refusing recovery'))
    for unit in ('s2l', 'xl2tpd', 's2l-guard', 's2l-ipsec'):
        stop_and_verify(unit)
    # Guards may be removed only after the tunnel daemon and its PPP children stop.
    if run('pgrep', '-x', 'pppd', check=False).returncode != 1:
        raise RuntimeError(tr('PPP sessions still exist; leak guards retained for manual recovery'))
    if state.get('session_guard') is True:
        if 'table ip s2l_ppp_sessions' in run('nft', 'list', 'tables').stdout.splitlines():
            run('nft', 'delete', 'table', 'ip', 's2l_ppp_sessions')
        mark = '0x40000000'
        comment = ('-m', 'comment', '--comment', 's2l:session-admission')
        for direction in ('-i', '-o'):
            for table, rule in (
                ('mangle', (*comment, '-j', 'MARK', '--set-xmark', f'0x0/{mark}')),
                ('filter', ('-m', 'mark', '!', '--mark', f'{mark}/{mark}', *comment, '-j', 'DROP')),
            ):
                command = ('iptables', '-w', '5', '-t', table)
                args = ('FORWARD', direction, 'ppp+', *rule)
                if run(*command, '-C', *args, check=False).returncode == 0:
                    run(*command, '-D', *args)
    for binary, chain, target in (
        ('iptables', 'FORWARD', 'S2L_FORWARD'), ('iptables', 'INPUT', 'S2L_MANAGEMENT'),
        ('iptables', 'INPUT', 'DROP'), ('ip6tables', 'INPUT', 'DROP'), ('ip6tables', 'FORWARD', 'DROP'),
    ):
        for _ in range(8):
            if run(binary, '-w', '5', '-C', chain, '-i', 'ppp+', '-j', target, check=False).returncode:
                break
            run(binary, '-w', '5', '-D', chain, '-i', 'ppp+', '-j', target)
    # Guard-only creates the return chain even before any mapping exists.
    # A partial installation can also have exact managed TUN entry hooks.
    # Remove those hooks only after PPP is gone; never flush built-in chains.
    for line in run('iptables', '-w', '5', '-S', 'FORWARD').stdout.splitlines():
        match = re.fullmatch(r'-A FORWARD -i ([A-Za-z0-9_.-]{1,15}) -j S2L_RETURN', line.strip())
        if match:
            run('iptables', '-w', '5', '-D', 'FORWARD', '-i', match[1], '-j', 'S2L_RETURN')
    for chain in ('S2L_FORWARD', 'S2L_RETURN', 'S2L_MANAGEMENT'):
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
            raise RuntimeError(tr('invalid snapshot kind'))
    if APP.exists():
        shutil.rmtree(APP)  # Fixed absolute path, validated ownership above.
    if state.get('manual_apt') is True and not state.get('apt_preexisting') and matches_apt_policy():
        APT_POLICY.unlink()
    for key, value in state['sysctl'].items():
        if value not in ('0', '1', '2'):
            raise RuntimeError(tr('invalid sysctl snapshot'))
        run('sysctl', '-w', key + '=' + value)
    if state.get('ntp') in ('yes', 'no'):
        run('timedatectl', 'set-ntp', 'true' if state['ntp'] == 'yes' else 'false')
    run('systemctl', 'daemon-reload')
    for name, previous in state['units'].items():
        run('systemctl', 'enable' if previous['enabled'] else 'disable', name, check=False)
        if previous['active']:
            run('systemctl', 'start', name)
            if name == 'nginx':
                # Starting an already active unit leaves the failed install's
                # in-memory HTTPS listeners in place. Reload the restored files.
                run('systemctl', 'reload', name)
        else:
            run('systemctl', 'stop', name, check=False)
    clear()
    print(tr('安装未完成，已恢复安装前配置；依赖软件包保留，可重新执行安装。'))


def clear():
    for path in ROOT.iterdir():
        if path.is_file() and not path.is_symlink():
            path.unlink()


def commit():
    secure_root()
    state = json.loads((ROOT / 'journal.json').read_text())
    if state.get('manual_apt') is True:
        if state.get('apt_preexisting'):
            if not matches_apt_policy():
                raise RuntimeError(tr('unexpected installation target: ') + str(APT_POLICY))
        else:
            fd, temporary = tempfile.mkstemp(prefix='.s2l-apt-', dir=APT_POLICY.parent)
            try:
                with os.fdopen(fd, 'wb') as out:
                    os.fchmod(out.fileno(), 0o644)
                    out.write(APT_POLICY_TEXT.encode())
                    out.flush()
                    os.fsync(out.fileno())
                os.link(temporary, APT_POLICY)
            finally:
                Path(temporary).unlink(missing_ok=True)
        effective = run('apt-config', 'shell', 'periodic', 'APT::Periodic::Unattended-Upgrade',
                        'shutdown', 'Unattended-Upgrade::InstallOnShutdown',
                        'reboot', 'Unattended-Upgrade::Automatic-Reboot').stdout.splitlines()
        if not {"periodic='0'", "shutdown='false'", "reboot='false'"}.issubset(effective):
            raise RuntimeError('APT settings override the S2L manual maintenance policy: ' + str(APT_POLICY))
    state['complete'] = True
    save(state)
    (APP / '.install-owner').unlink(missing_ok=True)
    clear()


if __name__ == '__main__':
    if os.geteuid() != 0:
        raise SystemExit(tr('requires root'))
    try:
        {'begin': begin, 'recover': recover, 'commit': commit}[sys.argv[1]]()
    except Exception as error:
        raise SystemExit(str(error))

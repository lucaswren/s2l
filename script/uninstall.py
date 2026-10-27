#!/usr/bin/env python3
"""Uninstall the default managed layout, with a private backup and scoped cleanup."""
import argparse
from contextlib import ExitStack
from datetime import datetime, timezone
import ipaddress
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile

APP = Path('/opt/s2l')
CONFIG = APP / 'config.json'
SITE = Path('/etc/nginx/conf.d/s2l.conf')
CHAP = Path('/etc/ppp/chap-secrets')
L2TP = Path('/etc/xl2tpd/xl2tpd.conf')
SECRET = Path('/etc/ipsec.secrets')
ARMOR = Path('/etc/apparmor.d/local/usr.lib.ipsec.charon')
ARMOR_LINE = '"/opt/s2l/ipsec.secrets" r, # s2l-managed-secret'
UNITS = ('s2l', 's2l-ipsec', 's2l-guard')
CHAINS = {'S2L_FORWARD', 'S2L_RETURN', 'S2L_MANAGEMENT', 'S2L-IPSEC', 'S2L-IPSEC-OUT', 'S2L-IPSEC-PEERS'}
LANG = 'zh-Hans'
CACHES = {
    Path('/var/lib/s2l/web-update'): {'job.json', 'release.json', 'control.lock', 'worker.lock'},
    Path('/run/s2l-ipsec-nat'): {'state.json', 'lock'},
}


def message(en, hant, hans):
    return {'en': en, 'zh-Hant': hant, 'zh-Hans': hans}[LANG]


def run(*args, check=True, input=None, timeout=30):
    result = subprocess.run(args, input=input, capture_output=True, text=True, timeout=timeout)
    if check and result.returncode:
        # Never include captured command output: configuration may contain secrets.
        raise RuntimeError(f'{args[0]} {args[1] if len(args) > 1 else ""}: exit {result.returncode}')
    return result


def safe_path(path):
    for parent in (path, *path.parents):
        if parent.is_symlink():
            raise RuntimeError('symlink target requires manual uninstall: ' + str(parent))
    if path.exists() and not path.is_file():
        raise RuntimeError('unexpected target type: ' + str(path))


def read_file(path):
    safe_path(path)
    return path.read_bytes() if path.exists() else None


def strip_chap(text):
    lines, output = text.splitlines(keepends=True), []
    index = 0
    while index < len(lines):
        if lines[index].strip().startswith('# s2l:'):
            if not re.fullmatch(r'# s2l:[A-Za-z0-9_.-]{1,128}', lines[index].strip()) or index + 1 >= len(lines):
                raise RuntimeError('invalid managed CHAP marker')
            account = shlex.split(lines[index + 1], comments=True)
            if len(account) != 4 or account[1] != '*' or account[3] == '*':
                raise RuntimeError('invalid managed CHAP entry')
            ipaddress.IPv4Address(account[3])
            index += 2
        else:
            output.append(lines[index])
            index += 1
    return ''.join(output)


def strip_l2tp(text):
    begin, end = '; BEGIN s2l managed block - DO NOT EDIT', '; END s2l managed block'
    lines = text.splitlines(keepends=True)
    if sum(line.strip() == begin for line in lines) != 1 or sum(line.strip() == end for line in lines) != 1:
        raise RuntimeError('xl2tpd.conf is not exclusively managed by S2L')
    inside, finished, output = False, False, []
    for line in lines:
        if line.strip() == begin:
            inside = True
        elif line.strip() == end:
            if not inside:
                raise RuntimeError('invalid xl2tpd managed block')
            inside, finished = False, True
        elif not inside:
            if line.strip() and not line.lstrip().startswith((';', '#')):
                raise RuntimeError('foreign xl2tpd configuration; use manual uninstall')
            output.append(line)
    if inside or not finished:
        raise RuntimeError('invalid xl2tpd managed block')
    return ''.join(output)


def firewall_plan(snapshot, ipv6=False):
    """Return noflush transactions. Only exact owned hooks/comments/chains qualify."""
    table, tables = None, {}
    for line in snapshot.splitlines():
        if line.startswith('*'):
            table = line[1:]
            tables[table] = {'chains': [], 'delete': []}
        elif line == 'COMMIT':
            table = None
        elif table and line.startswith(':'):
            chain = line[1:].split()[0]
            if table == 'filter' and not ipv6 and chain in CHAINS:
                tables[table]['chains'].append(chain)
        elif table and line.startswith('-A '):
            row = shlex.split(line)
            if table == 'filter' and not ipv6 and row[1] in CHAINS:
                continue
            target = row[row.index('-j') + 1] if '-j' in row else ''
            owned = table == 'filter' and row in [
                ['-A', 'INPUT', '-i', 'ppp+', '-j', 'DROP'],
                *([['-A', 'FORWARD', '-i', 'ppp+', '-j', 'DROP']] if ipv6 else [
                    ['-A', 'INPUT', '-i', 'ppp+', '-j', 'S2L_MANAGEMENT'],
                    ['-A', 'FORWARD', '-i', 'ppp+', '-j', 'S2L_FORWARD'],
                    ['-A', 'INPUT', '-j', 'S2L-IPSEC'],
                    ['-A', 'OUTPUT', '-j', 'S2L-IPSEC-OUT'],
                ])]
            if not ipv6:
                owned |= table == 'filter' and len(row) == 6 and row[:3] == ['-A', 'FORWARD', '-i'] and bool(re.fullmatch(r'[A-Za-z0-9_.-]{1,15}', row[3])) and row[4:] == ['-j', 'S2L_RETURN']
                comment = row[row.index('--comment') + 1] if '--comment' in row else ''
                owned |= bool(re.fullmatch(r's2l:[A-Za-z0-9_.-]{1,160}', comment)) or comment in ('s2l-tcpmss', 's2l-ipsec-nat')
                if target in CHAINS and not owned:
                    raise RuntimeError('foreign firewall reference to ' + target)
                if '-g' in row and row[row.index('-g') + 1] in CHAINS:
                    raise RuntimeError('foreign firewall goto to an S2L chain')
            if owned:
                tables[table]['delete'].append('-D' + line[2:])
    if table is not None or not tables:
        raise RuntimeError('incomplete firewall snapshot')
    payload = []
    for name, data in tables.items():
        if data['chains'] or data['delete']:
            payload += ['*' + name, *data['delete']]
            payload += ['-F ' + chain for chain in data['chains']]
            payload += ['-X ' + chain for chain in data['chains']]
            payload.append('COMMIT')
    return '\n'.join(payload) + '\n' if payload else ''


def check_connection(mappings):
    connection = os.environ.get('SSH_CONNECTION') or os.environ.get('SSH_CLIENT')
    if not connection:
        return
    peer = str(ipaddress.ip_address(connection.split()[0]))
    routes = json.loads(run('ip', '-j', 'route', 'get', peer).stdout)
    tunnels = {mapping['tun_name'] for mapping in mappings}
    if not routes or any(row.get('dev', '').startswith('ppp') or row.get('dev') in tunnels for row in routes):
        raise RuntimeError(message('SSH uses this VPN path; use public SSH or the provider console.', 'SSH 使用此 VPN 路徑；請改用公網 SSH 或主機商控制台。', 'SSH 使用此 VPN 路径；请改用公网 SSH 或主机商控制台。'))


def plan():
    from verify_update import preflight
    config = preflight(CONFIG)
    for path in (APP, APP / 'data'):
        if path.is_symlink() or path.resolve() != path:
            raise RuntimeError('custom installation directory requires manual uninstall')
    for directory, dirs, _ in os.walk(APP, followlinks=False):
        for name in dirs:
            path = Path(directory) / name
            if not path.is_symlink() and os.path.ismount(path):
                raise RuntimeError('mount inside installation directory: ' + str(path))
    if os.path.ismount(APP):
        raise RuntimeError('installation directory is a mount point')
    safe_path(APP / 'data/state.json')
    run(str(APP / 's2l'), '-config', str(CONFIG), '-check-uninstall')
    state_path = APP / 'data/state.json'
    state = json.loads(state_path.read_text()) if state_path.exists() else {}
    mappings = state.get('mappings') or []
    check_connection(mappings)
    for unit in ('s2l-web-update', 's2l-cert-renew', 's2l-cert-renew.timer'):
        if run('systemctl', 'is-active', '--quiet', unit, check=False).returncode == 0:
            raise RuntimeError('update/certificate task is active; finish it first: ' + unit)
    if Path('/var/lib/s2l-install/journal.json').exists():
        raise RuntimeError('recover the incomplete installation before uninstalling')
    for directory, names in CACHES.items():
        if directory.is_symlink() or directory.resolve() != directory or os.path.ismount(directory):
            raise RuntimeError('custom runtime directory: ' + str(directory))
        if directory.exists():
            for path in directory.iterdir():
                safe_path(path)
                if path.name not in names:
                    raise RuntimeError('unknown runtime file: ' + str(path))
    expected = {
        Path('/usr/local/bin/s2l'): b'APP_NAME="s2l"',
        Path('/etc/systemd/system/s2l.service'): b'ExecStart=/opt/s2l/s2l -config /opt/s2l/config.json',
        Path('/etc/systemd/system/s2l-guard.service'): b'ExecStart=/opt/s2l/s2l -guard-only',
        Path('/etc/systemd/system/s2l-ipsec.service'): b'--conf /opt/s2l/ipsec.conf',
        Path('/etc/systemd/system/xl2tpd.service.d/s2l.conf'): b'Requires=s2l-guard.service',
        Path('/etc/sysctl.d/99-s2l.conf'): b'net.ipv4.ip_forward=1',
        Path('/etc/ppp/options.xl2tpd'): b'require-mschap-v2',
        Path('/etc/ppp/options.xl2tpd.s2l-up'): b'/run/s2l/ppp-events.sock',
        Path('/etc/ppp/options.xl2tpd.s2l-down'): b'/run/s2l/ppp-events.sock',
        SITE: b'# Managed by s2l HTTPS',
    }
    remove, edits, originals = [], {}, {}
    for path, marker in expected.items():
        content = read_file(path)
        if content is not None:
            if marker not in content:
                raise RuntimeError('unmanaged installation file: ' + str(path))
            remove.append(path)
            originals[path] = content
    for suffix in ('service', 'timer'):
        path = Path('/etc/systemd/system/s2l-cert-renew.' + suffix)
        content = read_file(path)
        if content is not None:
            if b's2l' not in content:
                raise RuntimeError('unmanaged certificate unit')
            remove.append(path)
            originals[path] = content
    # Removing the app is safe only if the managed service and LNS prove ownership.
    if Path('/etc/systemd/system/s2l.service') not in originals:
        raise RuntimeError('managed S2L unit is missing')
    for path, transform in (
        (CHAP, strip_chap), (L2TP, strip_l2tp),
        (SECRET, lambda text: ''.join(line for line in text.splitlines(keepends=True) if line.strip() != 'include /opt/s2l/ipsec.secrets')),
        (ARMOR, lambda text: ''.join(line for line in text.splitlines(keepends=True) if line.strip() != ARMOR_LINE)),
    ):
        content = read_file(path)
        if content is None:
            if path == L2TP:
                raise RuntimeError('managed xl2tpd configuration is missing')
            continue
        originals[path] = content
        replacement = transform(content.decode()).encode()
        if replacement != content:
            edits[path] = replacement
    for unit in (*UNITS, 'xl2tpd'):
        directory = Path('/etc/systemd/system') / (unit + '.service.d')
        if directory.is_symlink() or (directory.exists() and any(path != Path('/etc/systemd/system/xl2tpd.service.d/s2l.conf') for path in directory.iterdir())):
            raise RuntimeError('custom service overrides require manual uninstall: ' + unit)
    for binary in ('iptables', 'ip6tables'):
        firewall_plan(run(binary + '-save').stdout, binary == 'ip6tables')
    if SITE in remove:
        run('nginx', '-t')
    return config, mappings, remove, edits, originals


def backup_to(directory, originals):
    temporary = directory / 'backup.partial'
    with tarfile.open(temporary, 'w:gz', dereference=False) as archive:
        archive.add(APP, arcname='opt/s2l')
        for path in originals:
            archive.add(path, arcname=path.as_posix().lstrip('/'))
        for path in CACHES:
            if path.exists():
                archive.add(path, arcname=path.as_posix().lstrip('/'))
    os.chmod(temporary, 0o600)
    with temporary.open('rb') as stream:
        os.fsync(stream.fileno())
    temporary.replace(directory / 'backup.tar.gz')


def write_file(path, content):
    mode = stat.S_IMODE(path.stat().st_mode)
    descriptor, temporary = tempfile.mkstemp(prefix='.s2l-uninstall-', dir=path.parent)
    try:
        with os.fdopen(descriptor, 'wb') as stream:
            os.fchmod(stream.fileno(), mode)
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


def uninstall(current):
    config, mappings, remove, edits, originals = current
    backup_root = Path('/var/backups')
    if backup_root.is_symlink():
        raise RuntimeError('backup directory must not be a symlink')
    backup_root.mkdir(parents=True, exist_ok=True)
    backup = Path(tempfile.mkdtemp(prefix='s2l-uninstall-' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ-'), dir=backup_root))
    os.chmod(backup, 0o700)
    backup_to(backup, originals)  # Backup failure cannot stop a running service.
    for binary in ('iptables', 'ip6tables'):
        (backup / (binary + '.save')).write_text(run(binary + '-save').stdout)
    print(message('Private backup: ', '私有備份：', '私有备份：') + str(backup), flush=True)
    try:
        # Stop admission first; keep guards until PPP, TUN and IPsec are gone.
        run('systemctl', 'stop', 'xl2tpd', timeout=60)
        run('systemctl', 'stop', 's2l', timeout=90)
        run('systemctl', 'stop', 's2l-ipsec', timeout=45)
        for unit in ('xl2tpd', 's2l', 's2l-ipsec'):
            status = run('systemctl', 'show', unit, '--property=ActiveState', '--value').stdout.strip()
            if status not in ('inactive', 'failed'):
                raise RuntimeError('service did not stop: ' + unit)
        ppp = run('pgrep', '-x', 'pppd', check=False)
        if ppp.returncode != 1:
            raise RuntimeError('PPP processes remain or inspection failed; guards retained')
        # Revalidate file ownership after stopping API writes, before cleanup.
        _, mappings, remove, edits, originals = plan()
        backup_to(backup, originals)
        run(sys.executable, str(APP / 'ipsec_nat.py'), 'cleanup')
        run(str(APP / 's2l'), '-config', str(CONFIG), '-cleanup-network', timeout=90)
        # Test every table transaction before removing any global guard.
        plans = [(binary, firewall_plan(run(binary + '-save').stdout, binary == 'ip6tables')) for binary in ('iptables', 'ip6tables')]
        for binary, payload in plans:
            if payload:
                run(binary + '-restore', '--test', '--noflush', input=payload)
        for binary, payload in plans:
            if payload:
                run(binary + '-restore', '--wait', '5', '--noflush', input=payload)
        for binary, _ in plans:
            if firewall_plan(run(binary + '-save').stdout, binary == 'ip6tables'):
                raise RuntimeError('owned firewall state remains')
        # Remove only the managed HTTPS site; retain Nginx and other sites.
        if SITE in remove:
            SITE.unlink()
            try:
                run('nginx', '-t')
                if run('systemctl', 'is-active', '--quiet', 'nginx', check=False).returncode == 0:
                    run('systemctl', 'reload', 'nginx')
            except Exception:
                SITE.write_bytes(originals[SITE])
                raise
            remove.remove(SITE)
        for path, content in edits.items():
            write_file(path, content)
        if ARMOR in edits and Path('/sys/module/apparmor/parameters/enabled').exists() and Path('/sys/module/apparmor/parameters/enabled').read_text().strip().lower() == 'y':
            run('apparmor_parser', '-r', '/etc/apparmor.d/usr.lib.ipsec.charon')
        run('systemctl', 'disable', '--now', 's2l-guard')
        run('systemctl', 'disable', 'xl2tpd', 's2l', 's2l-ipsec')
        for suffix in ('service', 'timer'):
            if Path('/etc/systemd/system/s2l-cert-renew.' + suffix) in remove:
                run('systemctl', 'disable', '--now', 's2l-cert-renew.' + suffix)
        for path in remove:
            if read_file(path) != originals[path]:
                raise RuntimeError('installation file changed during uninstall: ' + str(path))
        for path in remove:
            path.unlink()
        run('systemctl', 'daemon-reload')
        run('systemctl', 'reset-failed', 's2l', 's2l-guard', 's2l-ipsec', check=False)
        if APP != Path('/opt/s2l') or APP.is_symlink() or APP.resolve() != APP:
            raise RuntimeError('installation path changed; data retained')
        shutil.rmtree(APP)
        for directory, names in CACHES.items():
            if directory.is_symlink() or directory.resolve() != directory or os.path.ismount(directory):
                raise RuntimeError('runtime directory changed; retained: ' + str(directory))
            if directory.exists():
                for path in directory.iterdir():
                    safe_path(path)
                    if path.name not in names:
                        raise RuntimeError('unknown runtime file; retained: ' + str(path))
                    path.unlink()
                directory.rmdir()
        print(message('S2L uninstalled. Keep the private backup; it contains credentials and certificates.', 'S2L 已解除安裝。請保管私有備份，其中包含帳號與憑證。', 'S2L 已卸载。请保管私有备份，其中包含账号和证书。'))
    except Exception:
        print(message('Uninstall incomplete. Services may be stopped; inspect the backup and error before retrying. No automatic restart.', '解除安裝未完成。服務可能已停止；請檢查備份與錯誤後重試，不會自動重啟。', '卸载未完成。服务可能已停止；请检查备份与错误后重试，不会自动重启。'), file=sys.stderr)
        raise


def main():
    global LANG
    parser = argparse.ArgumentParser(description='Uninstall managed S2L with a private backup; confirmation required.')
    parser.add_argument('--dry-run', action='store_true', help='inspect only; do not stop services or write files')
    parser.add_argument('--lang', choices=('en', 'zh-Hant', 'zh-Hans'))
    args = parser.parse_args()
    if sys.platform != 'linux' or os.geteuid() != 0:
        raise RuntimeError('requires Linux root')
    os.umask(0o077)
    safe_path(CONFIG)
    language = json.loads(CONFIG.read_text()).get('ui_language', '')
    LANG = args.lang or {'en': 'en', 'zh-TW': 'zh-Hant', 'zh-CN': 'zh-Hans'}.get(language, 'zh-Hans')
    current = plan()
    print(message('Uninstall S2L: stop all managed VPN sessions, back up and remove /opt/s2l, its shortcut, service units, HTTPS site and owned network rules.', '解除安裝 S2L：停止受管 VPN 連線，備份並刪除 /opt/s2l、快捷指令、服務、HTTPS 站台與受管網路規則。', '卸载 S2L：停止受管 VPN 连接，备份并删除 /opt/s2l、快捷命令、服务、HTTPS 站点与受管网络规则。'))
    print(message('Keep SSH settings, kernels, dependencies, source checkouts and other firewall rules. Current sysctl values remain until separately reviewed or rebooted.', '保留 SSH 設定、核心、相依套件、原始碼及其他防火牆規則。目前 sysctl 值保留，另行審閱或重啟後生效。', '保留 SSH 设置、内核、依赖、源码及其他防火墙规则。当前 sysctl 值保留，另行审阅或重启后生效。'))
    if args.dry_run:
        print(message('Preview complete; nothing changed.', '預覽完成，未變更任何設定。', '预览完成，未更改任何设置。'))
        return
    answer = input(message('VPN traffic will disconnect. Type UNINSTALL to continue: ', 'VPN 流量將中斷。輸入 UNINSTALL 繼續：', 'VPN 流量将中断。输入 UNINSTALL 继续：'))
    if answer != 'UNINSTALL':
        print(message('Cancelled; nothing changed.', '已取消，未變更。', '已取消，未更改。'))
        return
    import fcntl
    with ExitStack() as locks:
        for name in ('s2l-bootstrap', 's2l-install', 's2l-update', 's2l-kernel'):
            fd = os.open('/run/' + name + '.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
            stream = locks.enter_context(os.fdopen(fd, 'r+'))
            try:
                fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError as error:
                raise RuntimeError('another install/update/kernel operation is running') from error
        fd = os.open(APP / 'ipsec.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        stream = locks.enter_context(os.fdopen(fd, 'r+'))
        fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
        uninstall(plan())


if __name__ == '__main__':
    try:
        main()
    except (EOFError, KeyboardInterrupt):
        print('\nCancelled.', file=sys.stderr)
        sys.exit(1)
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print('[ERROR] ' + str(error), file=sys.stderr)
        sys.exit(1)

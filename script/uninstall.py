#!/usr/bin/env python3
"""Remove the default managed layout; retain recovery data only on failure."""
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
SOURCE = Path('/opt/src/s2l')
BACKUPS = Path('/var/backups')
ROOT_HOME = Path('/root')
CACHES = {
    Path('/var/lib/s2l/web-update'): {'job.json', 'release.json', 'control.lock', 'worker.lock'},
    Path('/run/s2l-ipsec-nat'): {'state.json', 'lock'},
    Path('/var/lib/s2l-dependencies'): {'receipt.json'},
}


def checked_tree(path):
    """Validate the full deletion boundary without following internal symlinks."""
    if path.is_symlink() or path.resolve() != path or not path.is_dir():
        raise RuntimeError('unexpected cleanup directory: ' + str(path))
    for directory, dirs, files in os.walk(path, followlinks=False):
        if os.path.ismount(directory):
            raise RuntimeError('mount inside cleanup directory: ' + directory)
        for name in dirs + files:
            item = Path(directory) / name
            if not item.is_symlink() and os.path.ismount(item):
                raise RuntimeError('mounted cleanup target: ' + str(item))
    return path


def source_tree():
    if not SOURCE.exists() and not SOURCE.is_symlink():
        return None
    checked_tree(SOURCE)
    safe_path(SOURCE / 'go.mod')
    if 'module github.com/lucaswren/s2l\n' not in (SOURCE / 'go.mod').read_text():
        raise RuntimeError('unrecognized source checkout: ' + str(SOURCE))
    # Only the default, clean standalone checkout belongs to this operation.
    # Ignored custom files count as user data; only known build output qualifies.
    if not (SOURCE / '.git').is_dir() or (SOURCE / '.git').is_symlink():
        raise RuntimeError('source is not a standalone Git checkout')
    if run('git', '-C', str(SOURCE), 'status', '--porcelain', '--untracked-files=all').stdout.strip():
        raise RuntimeError('source has local changes; save them before uninstalling')
    ignored = run('git', '-C', str(SOURCE), 'ls-files', '--others', '--ignored', '--exclude-standard', '-z').stdout.split('\0')
    for name in filter(None, ignored):
        if name != 'bin/s2l' and not re.fullmatch(r'(?:[A-Za-z0-9_.-]+/)*__pycache__/[A-Za-z0-9_.-]+\.pyc', name):
            raise RuntimeError('custom ignored source file: ' + str(SOURCE / name))
    return SOURCE


def backup_trees():
    """Recognize S2L backup formats, never delete by a broad name glob alone."""
    found = []
    for base, pattern, kind in ((ROOT_HOME, 's2l-backup.*', 'update'), (BACKUPS, 's2l-uninstall-*', 'uninstall'), (BACKUPS, 's2l-kernel-*', 'kernel')):
        if base.is_symlink() or base.resolve() != base:
            raise RuntimeError('custom backup root: ' + str(base))
        for path in sorted(base.glob(pattern)):
            checked_tree(path)
            names = {item.name for item in path.iterdir()}
            if kind == 'update':
                required = {'app', 'system', 'system-paths.txt', 'iptables.v4', 'iptables.v6', 'requested-ref.txt'}
                valid = names == required
                for item in ('app/config.json', 'system-paths.txt', 'iptables.v4', 'iptables.v6', 'requested-ref.txt'):
                    safe_path(path / item)
                    valid = valid and (path / item).is_file()
                if valid:
                    config = json.loads((path / 'app/config.json').read_text())
                    valid = isinstance(config, dict) and config.get('data_dir') in ('/opt/s2l/data', './data')
            elif kind == 'kernel':
                valid = {'grub', 'grub.cfg', 'running-kernel.txt'} <= names <= {'grub', 'grub.cfg', 'running-kernel.txt', 'grub.d'}
                for item in ('grub', 'grub.cfg', 'running-kernel.txt'):
                    safe_path(path / item)
                    valid = valid and (path / item).is_file()
                if valid:
                    valid = bool(re.fullmatch(r'[0-9][A-Za-z0-9.+_-]*\n?', (path / 'running-kernel.txt').read_text()))
            else:
                valid = 'backup.tar.gz' in names and names <= {'backup.tar.gz', 'backup.partial', 'iptables.save', 'ip6tables.save'}
                safe_path(path / 'backup.tar.gz')
                if valid:
                    with tarfile.open(path / 'backup.tar.gz') as bundle:
                        members = {entry.name for entry in bundle if entry.isfile()}
                        valid = {'opt/s2l/config.json', 'etc/systemd/system/s2l.service'} <= members
            if not valid:
                raise RuntimeError('unrecognized backup; inspect before uninstalling: ' + str(path))
            found.append(path)
    return found


def runtime_tree():
    path = Path('/run/s2l')
    if not path.exists() and not path.is_symlink():
        return None
    checked_tree(path)
    for item in path.iterdir():
        mode = item.lstat().st_mode
        valid = item.name == 'ppp-events.sock' and stat.S_ISSOCK(mode)
        valid |= bool(re.fullmatch(r's2l-[A-Za-z0-9_.-]{1,15}\.(json|log)', item.name)) and stat.S_ISREG(mode)
        if not valid:
            raise RuntimeError('unknown runtime file: ' + str(item))
    return path


def cleanup_inventory():
    targets = backup_trees()
    source = source_tree()
    if source:
        targets.append(source)
    runtime_tree()
    for path, allowed in ((Path('/var/lib/s2l'), {'web-update'}), (Path('/var/lib/s2l-install'), set())):
        if path.exists() or path.is_symlink():
            checked_tree(path)
            if any(item.name not in allowed for item in path.iterdir()):
                raise RuntimeError('unknown persistent file: ' + str(path))
    return targets


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


def check_stopped_links(mappings, links, firewall):
    tunnels = {mapping['tun_name'] for mapping in mappings}
    for line in firewall.splitlines():
        row = shlex.split(line)
        if len(row) == 6 and row[:3] == ['-A', 'FORWARD', '-i'] and row[4:] == ['-j', 'S2L_RETURN']:
            tunnels.add(row[3])
    if any(link.get('ifname', '').startswith('ppp') or link.get('ifname') in tunnels for link in links):
        raise RuntimeError('PPP or managed TUN remains; guards retained')


def plan():
    from verify_update import preflight
    import dependencies
    dependencies.read()
    config = preflight(CONFIG)
    targets = cleanup_inventory()
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
        Path('/etc/ppp/chap-secrets.s2l.tmp'): b'# s2l:',
        Path('/etc/xl2tpd/xl2tpd.conf.s2l.tmp'): b'; BEGIN s2l managed block',
        Path('/etc/ppp/options.xl2tpd.s2l.tmp'): b'# generated by s2l',
        SITE: b'# Managed by s2l HTTPS',
    }
    # An interrupted atomic rename can leave account passwords in a sidecar.
    # Hook temporaries have a reserved name plus the managed socket marker.
    expected.update({path: b'/run/s2l/ppp-events.sock' for path in Path('/etc/ppp').glob('.ppp-hook-*')})
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
    return config, mappings, remove, edits, originals, targets


def cleanup_proofs():
    for line in run('ip', 'netns', 'list').stdout.splitlines():
        name = line.split()[0]
        match = re.fullmatch(r's2l-proof-([1-9][0-9]*)-([0-9a-f]{10})', name)
        if not match:
            continue
        try:
            os.kill(int(match[1]), 0)
        except ProcessLookupError:
            pass
        else:
            raise RuntimeError('diagnostic namespace owner remains: ' + name)
        if run('ip', 'netns', 'pids', name).stdout.strip():
            raise RuntimeError('diagnostic namespace still in use: ' + name)
        device = 'pppst' + match[2]
        links = json.loads(run('ip', '-j', 'link', 'show').stdout)
        if any(link.get('ifname') == device for link in links):
            run('ip', 'link', 'del', device)
        run('ip', 'netns', 'del', name)


def verify_removed(paths, mappings):
    remaining = [str(path) for path in paths if path.exists() or path.is_symlink()]
    if remaining:
        raise RuntimeError('S2L files remain: ' + ', '.join(remaining))
    for binary in ('iptables', 'ip6tables'):
        if firewall_plan(run(binary + '-save').stdout, binary == 'ip6tables'):
            raise RuntimeError('S2L firewall rules remain')
    for kind in ('rule', 'route'):
        args = ['ip', '-N', '-j', kind, 'show']
        if kind == 'route':
            args += ['table', 'all']
        if any(str(row.get('protocol')) == '196' for row in json.loads(run(*args).stdout)):
            raise RuntimeError('S2L policy routes/rules remain')
    check_stopped_links(mappings, json.loads(run('ip', '-j', 'link', 'show').stdout), '')
    if any(re.match(r's2l-proof-[1-9][0-9]*-[0-9a-f]{10}(?: |$)', line) for line in run('ip', 'netns', 'list').stdout.splitlines()):
        raise RuntimeError('S2L diagnostic namespaces remain')
    for path, marker in ((CHAP, b'# s2l:'), (L2TP, b'; BEGIN s2l managed block'), (SECRET, b'include /opt/s2l/ipsec.secrets'), (ARMOR, ARMOR_LINE.encode())):
        if marker in (read_file(path) or b''):
            raise RuntimeError('S2L configuration remains: ' + str(path))
    for unit in (*UNITS, 'xl2tpd', 's2l-web-update', 's2l-cert-renew', 's2l-cert-renew.timer'):
        result = run('systemctl', 'show', unit, '--property=ActiveState', '--value', check=False)
        if result.stdout.strip() not in ('inactive', 'failed'):
            raise RuntimeError('service remains active or cannot be inspected: ' + unit)


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
    config, mappings, remove, edits, originals, targets = current
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
        _, mappings, remove, edits, originals, targets = plan()
        backup_to(backup, originals)
        cleanup_proofs()
        check_stopped_links(mappings, json.loads(run('ip', '-j', 'link', 'show').stdout), run('iptables-save').stdout)
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
        removed_paths = [*remove, APP, *CACHES, Path('/run/s2l'), Path('/var/lib/s2l'), Path('/var/lib/s2l-install')]
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
            if path == L2TP and not content.strip():
                path.unlink()
                removed_paths.append(path)
            else:
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
        import dependencies
        dependencies.cleanup(message)
        run('systemctl', 'daemon-reload')
        run('systemctl', 'reset-failed', 's2l', 's2l-guard', 's2l-ipsec', check=False)
        # Revalidate before deleting stored data, source or old recovery copies.
        # Keep the current backup until the final residual check has passed.
        targets = cleanup_inventory()
        os.chdir('/')
        for path in targets:
            if path != backup:
                shutil.rmtree(checked_tree(path))
                removed_paths.append(path)
        shutil.rmtree(checked_tree(APP))
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
        runtime = runtime_tree()
        if runtime:
            shutil.rmtree(runtime)
        for path in (Path('/var/lib/s2l'), Path('/var/lib/s2l-install'), *(Path('/etc/systemd/system') / (unit + '.service.d') for unit in (*UNITS, 'xl2tpd'))):
            if path.exists():
                checked_tree(path).rmdir()  # Empty only; unexpected files stop cleanup.
        verify_removed(removed_paths, mappings)
        if backup_trees() != [backup]:
            raise RuntimeError('unexpected backup appeared during uninstall')
        shutil.rmtree(checked_tree(backup))
        if backup.exists():
            raise RuntimeError('recovery backup still exists')
    except Exception:
        print(message('Uninstall incomplete. Services may be stopped; inspect the backup and error before retrying. No automatic restart.', '解除安裝未完成。服務可能已停止；請檢查備份與錯誤後重試，不會自動重啟。', '卸载未完成。服务可能已停止；请检查备份与错误后重试，不会自动重启。'), file=sys.stderr)
        raise


def main():
    global LANG
    parser = argparse.ArgumentParser(description='Remove managed S2L and its backups; confirmation required.')
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
    print(message('Remove S2L: VPN sessions, program, accounts, certificates, services, HTTPS site, owned rules, runtime data, default source and recognized S2L backups. Recovery backup is deleted after verification; retained only on failure.', '移除 S2L：VPN 連線、程式、帳號、憑證、服務、HTTPS 站台、受管規則、執行資料、預設原始碼及已辨識的 S2L 備份。復原備份在檢查通過後刪除，僅失敗時保留。', '清除 S2L：VPN 连接、程序、账号、证书、服务、HTTPS 站点、受管规则、运行数据、默认源码和已识别的 S2L 备份。恢复备份在检查通过后删除，仅失败时保留。'))
    print(message('Keep SSH settings, kernels, shared dependencies, system journal and other firewall rules. Current sysctl values remain until separately reviewed or rebooted.', '保留 SSH 設定、核心、共用相依套件、系統日誌及其他防火牆規則。目前 sysctl 值保留，另行審閱或重啟後生效。', '保留 SSH 设置、内核、共享依赖、系统日志及其他防火墙规则。当前 sysctl 值保留，另行审阅或重启后生效。'))
    print(message('Recorded dedicated VPN packages and sing-box are removed after shared-use checks. Unrecorded or shared dependencies are retained and reported.', '已記錄的專用 VPN 套件及 sing-box 通過共用檢查後清除；無記錄或共用的相依套件保留並提示。', '已记录的专用 VPN 软件包及 sing-box 通过共享检查后清除；无记录或共享的依赖保留并提示。'))
    for path in current[5]:
        print('  ' + str(path))
    if args.dry_run:
        print(message('Preview complete; nothing changed.', '預覽完成，未變更任何設定。', '预览完成，未更改任何设置。'))
        return
    answer = input(message('VPN traffic will disconnect. Type UNINSTALL to continue: ', 'VPN 流量將中斷。輸入 UNINSTALL 繼續：', 'VPN 流量将中断。输入 UNINSTALL 继续：'))
    if answer != 'UNINSTALL':
        print(message('Cancelled; nothing changed.', '已取消，未變更。', '已取消，未更改。'))
        return
    import fcntl
    with ExitStack() as locks:
        lock_files = []
        for name in ('s2l-bootstrap', 's2l-install', 's2l-update', 's2l-kernel'):
            fd = os.open('/run/' + name + '.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
            stream = locks.enter_context(os.fdopen(fd, 'r+'))
            lock_files.append((Path('/run/' + name + '.lock'), stream))
            try:
                fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError as error:
                raise RuntimeError('another install/update/kernel operation is running') from error
        fd = os.open(APP / 'ipsec.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        stream = locks.enter_context(os.fdopen(fd, 'r+'))
        fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
        uninstall(plan())
        # All cleanup and verification is complete. Release the empty lock
        # pathnames last; every cooperating caller uses nonblocking flock.
        for path, stream in lock_files:
            if path.stat().st_ino != os.fstat(stream.fileno()).st_ino:
                raise RuntimeError('operation lock changed during uninstall')
            path.unlink()
        print(message('S2L removed and verified. Its accounts, certificates, source, caches and backups have been deleted.', 'S2L 已移除並通過殘留檢查，帳號、憑證、原始碼、快取及備份已刪除。', 'S2L 已移除并通过残留检查，账号、证书、源码、缓存和备份已删除。'))


if __name__ == '__main__':
    try:
        main()
    except (EOFError, KeyboardInterrupt):
        print('\nCancelled.', file=sys.stderr)
        sys.exit(1)
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print('[ERROR] ' + str(error), file=sys.stderr)
        sys.exit(1)

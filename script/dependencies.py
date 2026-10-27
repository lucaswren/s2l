#!/usr/bin/env python3
"""Record dependencies and audit exclusive use, including upgrades without a receipt."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path('/var/lib/s2l-dependencies')
RECEIPT = ROOT / 'receipt.json'
SINGBOX = Path('/usr/local/bin/sing-box')
APP = Path('/opt/s2l')
PACKAGES = ('xl2tpd', 'ppp', 'strongswan', 'strongswan-starter', 'strongswan-charon', 'strongswan-libcharon', 'libstrongswan', 'libstrongswan-standard-plugins')
MANAGED = {'/etc/xl2tpd/xl2tpd.conf', '/etc/ppp/options.xl2tpd'}
CONFIG_ROOTS = ('/etc/ppp', '/etc/ipsec.d', '/etc/strongswan.d', '/etc/xl2tpd')
SERVICE_ROOTS = ('/etc/systemd/system', '/usr/lib/systemd/system', '/lib/systemd/system', '/etc/cron.d', '/etc/init.d')


def config_paths():
    return sorted(str(path) for root in CONFIG_ROOTS for path in Path(root).rglob('*') if path.is_symlink() or not path.is_dir())


def group(name):
    return 'l2tp' if name == 'xl2tpd' else 'ppp' if name == 'ppp' else 'ipsec'


def config_group(path):
    return 'ppp' if path.startswith('/etc/ppp/') else 'l2tp' if path.startswith('/etc/xl2tpd/') else 'ipsec'


def run(*args, check=True):
    return subprocess.run(args, check=check, text=True, capture_output=True, timeout=180,
                          env={**os.environ, 'LC_ALL': 'C', 'DEBIAN_FRONTEND': 'noninteractive', 'NEEDRESTART_MODE': 'l'})


def fingerprint(path):
    path = Path(path)
    if path.is_symlink():
        return 'link:' + os.readlink(path)
    if not path.exists():
        return 'missing'
    content = path.read_bytes()
    if str(path) in ('/etc/ppp/chap-secrets', '/etc/ipsec.secrets', '/etc/apparmor.d/local/usr.lib.ipsec.charon'):
        content = content.rstrip() + b'\n'
    return hashlib.sha256(content).hexdigest()


def installed(name):
    result = run('dpkg-query', '-W', '-f=${Status}', name, check=False)
    return result.returncode == 0 and result.stdout == 'install ok installed'


def read():
    if ROOT.is_symlink() or ROOT.resolve() != ROOT or RECEIPT.is_symlink():
        raise RuntimeError('unsafe dependency receipt path')
    for path in (ROOT, RECEIPT):
        if path.exists() and (path.stat().st_uid != os.geteuid() or path.stat().st_mode & 0o077):
            raise RuntimeError('dependency receipt must be private and owned by its installer')
    if not RECEIPT.exists():
        return None
    value = json.loads(RECEIPT.read_text())
    if not isinstance(value, dict) or value.get('version') != 1 or not isinstance(value.get('packages'), dict) or not isinstance(value.get('missing'), list):
        raise RuntimeError('invalid dependency receipt')
    if any(name not in PACKAGES for name in [*value['packages'], *value['missing']]):
        raise RuntimeError('unknown dependency in receipt')
    legacy = value.get('legacy_packages', [])
    if not isinstance(legacy, list) or any(name not in value['packages'] for name in legacy):
        raise RuntimeError('invalid legacy dependency inventory')
    return value


def save(value):
    ROOT.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = ROOT / 'receipt.tmp'
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as out:
        json.dump(value, out)
        out.flush()
        os.fsync(out.fileno())
    temporary.replace(RECEIPT)


def begin():
    # Survives first-install rollback; a retry must not claim pre-existing tools.
    if read() is None:
        save({'version': 1, 'missing': [name for name in PACKAGES if not installed(name)], 'packages': {}})


def record():
    value = read()
    if value is None:
        raise RuntimeError('missing initial dependency snapshot')
    for name in value['missing']:
        if name not in value['packages'] and installed(name):
            files = {}
            for line in run('dpkg-query', '-W', '-f=${Conffiles}', name).stdout.splitlines():
                fields = line.split()
                if fields and fields[0].startswith('/etc/'):
                    files[fields[0]] = fingerprint(fields[0])
            value['packages'][name] = files
    if 'config_paths' not in value:
        value['config_paths'] = config_paths()
    save(value)


def record_singbox():
    value = read()
    # Called only immediately after the installer creates a previously absent
    # executable, never when it reuses an existing installation.
    if value:
        if not SINGBOX.is_file() or SINGBOX.is_symlink():
            raise RuntimeError('invalid installed sing-box')
        value['singbox_sha256'] = fingerprint(SINGBOX)
        save(value)


def legacy_inventory():
    """Read-only reconstruction, using dpkg's original digests, never a snapshot
    that would silently bless existing foreign configuration as S2L-owned.
    Package candidates remain subject to a new sharing check at uninstall.
    """
    from verify_update import preflight
    config = preflight(APP / 'config.json')
    unit = Path('/etc/systemd/system/s2l.service')
    if unit.is_symlink() or b'ExecStart=/opt/s2l/s2l -config /opt/s2l/config.json' not in unit.read_bytes():
        raise RuntimeError('legacy dependency audit requires the managed S2L service')
    value = {'version': 1, 'missing': [], 'packages': {}, 'legacy_packages': [], 'config_paths': []}
    paths = set()
    for name in PACKAGES:
        if not installed(name):
            continue
        files = {}
        for line in run('dpkg-query', '-W', '-f=${Conffiles}', name).stdout.splitlines():
            fields = line.split()
            if len(fields) >= 2 and fields[0].startswith('/etc/') and re.fullmatch(r'[0-9a-f]{32}', fields[1]):
                files[fields[0]] = 'dpkg-md5:' + fields[1]
        paths.update(path for path in run('dpkg-query', '-L', name).stdout.splitlines() if path.startswith('/etc/'))
        value['packages'][name] = files
        value['legacy_packages'].append(name)
    value['config_paths'] = sorted(paths)
    binary = config.get('singbox_bin', 'sing-box')
    # The legacy installer used this exact standalone path. Identify its Go
    # module without executing the binary or downloading anything for cleanup.
    if binary in ('sing-box', str(SINGBOX)) and SINGBOX.is_file() and not SINGBOX.is_symlink() and SINGBOX.stat().st_size <= 64 * 1024 * 1024:
        data = SINGBOX.read_bytes()
        if data.startswith(b'\x7fELF') and b'github.com/sagernet/sing-box' in data.lower():
            value['singbox_sha256'] = fingerprint(SINGBOX)
            value['legacy_singbox'] = True
    return value


def migrate():
    if read() is None:
        save(legacy_inventory())


def matches_config(path, digest):
    if path in MANAGED:
        return True  # The uninstall coordinator validates and removes these.
    if not digest.startswith('dpkg-md5:'):
        return fingerprint(path) == digest
    file = Path(path)
    if file.is_symlink():
        return False
    if not file.exists():
        return True
    data = file.read_bytes()
    # S2L rewrites trailing newlines when removing its entries. Empty/comment
    # only credential files have no foreign accounts; actual entries retain PPP.
    if path in ('/etc/ppp/chap-secrets', '/etc/ipsec.secrets', '/etc/apparmor.d/local/usr.lib.ipsec.charon'):
        if all(not line.strip() or line.lstrip().startswith(b'#') for line in data.splitlines()):
            return True
    return hashlib.md5(data, usedforsecurity=False).hexdigest() == digest.removeprefix('dpkg-md5:')


def references(tokens, excluded=()):
    # Include disabled units and symlinked custom units, not only running ones.
    for root in SERVICE_ROOTS:
        for path in Path(root).rglob('*'):
            if not path.is_file():
                continue
            if path.name in excluded and (str(path.resolve().parent) in ('/usr/lib/systemd/system', '/lib/systemd/system') or str(path.parent) == '/etc/init.d'):
                continue
            if path.stat().st_size > 1024 * 1024:
                return True  # Cannot safely establish that a custom unit is unused.
            data = path.read_bytes()
            if any(token in data for token in tokens):
                return True
    for path in (Path('/etc/rc.local'), Path('/etc/crontab')):
        if path.is_file() and any(token in path.read_bytes() for token in tokens):
            return True
    return False


def sharing_groups(report):
    busy = set()
    for family, processes, units, tokens, excluded in (
        ('l2tp', ('xl2tpd',), ('xl2tpd',), (b'xl2tpd',), ('xl2tpd', 'xl2tpd.service')),
        ('ppp', ('pppd',), (), (b'pppd', b'pon ', b'poff '), ('pppd-dns', 'pppd-dns.service', 'xl2tpd', 'xl2tpd.service')),
        ('ipsec', ('charon', 'charon-systemd', 'starter'), ('strongswan-starter', 'strongswan', 'charon-systemd', 'ipsec'),
         (b'/usr/lib/ipsec', b'/usr/sbin/ipsec', b'charon'), ('ipsec', 'strongswan', 'strongswan-starter', 'strongswan.service', 'strongswan-starter.service', 'ipsec.service', 'charon-systemd.service')),
    ):
        if (any(run('systemctl', 'is-active', '--quiet', unit, check=False).returncode == 0 for unit in units)
                or any(run('pgrep', '-x', process, check=False).returncode != 1 for process in processes)
                or references(tokens, excluded)):
            report('shared service/process: ' + family)
            busy.add(family)
    return busy


def safe_removals(candidates, report):
    def simulation(names):
        result = run('apt-get', '-s', '--no-auto-remove', 'purge', *names)
        rows = result.stdout.splitlines()
        removed = {line.split()[1].split(':')[0] for line in rows if line.startswith(('Remv ', 'Purg '))}
        return removed if removed and not any(line.startswith('Inst ') for line in rows) else None
    if not candidates:
        return [], []
    allowed = set(candidates)
    removed = simulation(candidates)
    if removed is not None and removed <= allowed:
        return candidates, []
    # One shared library must not prevent removing unrelated dedicated tools.
    safe = [name for name in candidates if (result := simulation([name])) is not None and result <= allowed]
    removed = simulation(safe) if safe else None
    if removed is None or not removed <= set(safe):
        safe = []
    retained = [name for name in candidates if name not in safe]
    if retained:
        report('reverse dependencies require: ' + ', '.join(retained))
    return safe, retained


def package_plan(value, report=lambda text: None):
    candidates, retained = [], []
    busy = set()
    for name, files in value['packages'].items():
        if not installed(name):
            continue
        if not isinstance(files, dict) or any(not path.startswith('/etc/') or '..' in Path(path).parts for path in files):
            raise RuntimeError('invalid package file inventory')
        changed = [path for path, digest in files.items() if not matches_config(path, digest)]
        if changed:
            report('modified configuration: ' + ', '.join(changed))
            busy.add(group(name))
        candidates.append(name)
    extra = set(config_paths()) - set(value.get('config_paths', [])) - MANAGED
    if extra:
        report('additional configuration: ' + ', '.join(sorted(extra)))
        busy.update(config_group(path) for path in extra)
    busy.update(sharing_groups(report))
    retained = [name for name in candidates if group(name) in busy]
    candidates = [name for name in candidates if name not in retained]
    candidates, shared = safe_removals(candidates, report)
    return candidates, sorted(retained + shared)


def singbox_shared():
    for process in Path('/proc').iterdir():
        if process.name.isdigit():
            try:
                if (process / 'exe').resolve() == SINGBOX:
                    return True
            except (FileNotFoundError, ProcessLookupError):
                pass
    if references((b'sing-box',)):
        return True
    return any(Path(path).exists() for path in ('/etc/sing-box', '/usr/local/etc/sing-box'))


def cleanup(message=lambda en, hant, hans: en, legacy=None):
    value = read()
    if value is None:
        if legacy is None:
            raise RuntimeError('legacy installation must be audited before removing its managed service')
        value = legacy
        print(message('Auditing legacy dependencies for exclusive S2L use.', '正在核對舊版相依套件是否由 S2L 獨占使用。', '正在核对旧版依赖是否由 S2L 独占使用。'))
    unrecorded = [name for name in PACKAGES if name not in value['packages'] and installed(name)]
    if unrecorded:
        print(message('Retained pre-existing/unrecorded packages: ', '保留預先存在／無歸屬記錄的套件：', '保留预先存在／无归属记录的软件包：') + ', '.join(unrecorded))
    candidates, retained = package_plan(value, lambda reason: print('Dependency retention: ' + reason))
    if candidates:
        # Use dpkg for the final exact package list: it cannot expand removal
        # after the APT simulation and refuses newly introduced reverse deps.
        run('dpkg', '--purge', *candidates)
        if any(installed(name) for name in candidates):
            raise RuntimeError('dedicated VPN packages remain')
        print(message('Removed dedicated packages: ', '已清除專用套件：', '已清除专用软件包：') + ', '.join(candidates))
    if retained:
        print(message('Retained shared/configured packages: ', '保留共用／已修改設定的套件：', '保留共享／已修改配置的软件包：') + ', '.join(retained))
    expected = value.get('singbox_sha256')
    if expected and SINGBOX.exists():
        owner = run('dpkg-query', '-S', str(SINGBOX), check=False)
        if owner.returncode == 1 and re.fullmatch(r'[0-9a-f]{64}', expected) and not SINGBOX.is_symlink() and fingerprint(SINGBOX) == expected and not singbox_shared():
            SINGBOX.unlink()
            print(message('Removed dedicated sing-box binary.', '已刪除專用 sing-box 程式。', '已删除专用 sing-box 程序。'))
        else:
            print(message('Retained shared or changed sing-box binary.', '保留共用或已修改的 sing-box 程式。', '保留共享或已修改的 sing-box 程序。'))
    elif SINGBOX.exists() or SINGBOX.is_symlink():
        print(message('Retained pre-existing/unrecorded sing-box.', '保留預先存在／無歸屬記錄的 sing-box。', '保留预先存在／无归属记录的 sing-box。'))


if __name__ == '__main__':
    if os.geteuid() != 0:
        raise SystemExit('requires root')
    os.umask(0o077)
    {'begin': begin, 'record': record, 'singbox': record_singbox, 'migrate': migrate, 'cleanup': cleanup}[sys.argv[1]]()

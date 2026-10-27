#!/usr/bin/env python3
"""Record fresh-install ownership; remove only unchanged, unshared VPN dependencies."""
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
PACKAGES = ('xl2tpd', 'ppp', 'strongswan', 'strongswan-starter', 'strongswan-charon', 'libstrongswan', 'libstrongswan-standard-plugins')
MANAGED = {'/etc/xl2tpd/xl2tpd.conf', '/etc/ppp/options.xl2tpd'}
CONFIG_ROOTS = ('/etc/ppp', '/etc/ipsec.d', '/etc/strongswan.d', '/etc/xl2tpd')


def config_paths():
    return sorted(str(path) for root in CONFIG_ROOTS for path in Path(root).rglob('*') if not path.is_dir())


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
    if not RECEIPT.exists():
        return None
    value = json.loads(RECEIPT.read_text())
    if value.get('version') != 1 or not isinstance(value.get('packages'), dict) or not isinstance(value.get('missing'), list):
        raise RuntimeError('invalid dependency receipt')
    if any(name not in PACKAGES for name in [*value['packages'], *value['missing']]):
        raise RuntimeError('unknown dependency in receipt')
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


def package_plan(value):
    candidates, retained = [], []
    for name, files in value['packages'].items():
        if not installed(name):
            continue
        if not isinstance(files, dict) or any(not path.startswith('/etc/') or '..' in Path(path).parts for path in files):
            raise RuntimeError('invalid package file inventory')
        if any(path not in MANAGED and fingerprint(path) != digest for path, digest in files.items()):
            retained.append(name)
        else:
            candidates.append(name)
    # A retained VPN component can share all of its protocol libraries/tools.
    # Be conservative about configured peers, custom files and reverse deps.
    if set(config_paths()) - set(value.get('config_paths', [])) - MANAGED:
        return [], sorted(candidates + retained)
    if retained:
        return [], sorted(candidates + retained)
    if any(run('systemctl', 'is-active', '--quiet', unit, check=False).returncode == 0
           for unit in ('strongswan-starter', 'strongswan', 'charon-systemd', 'ipsec')):
        return [], sorted(candidates)
    if candidates:
        simulation = run('apt-get', '-s', '--no-auto-remove', 'purge', *candidates)
        removed = {line.split()[1].split(':')[0] for line in simulation.stdout.splitlines() if line.startswith(('Remv ', 'Purg '))}
        if not removed or not removed <= set(candidates) or any(line.startswith('Inst ') for line in simulation.stdout.splitlines()):
            return [], sorted(candidates)
    return candidates, []


def singbox_shared():
    for process in Path('/proc').iterdir():
        if process.name.isdigit():
            try:
                if (process / 'exe').resolve() == SINGBOX:
                    return True
            except (FileNotFoundError, ProcessLookupError):
                pass
    for root in ('/etc/systemd/system', '/usr/lib/systemd/system', '/etc/cron.d', '/etc/init.d'):
        for path in Path(root).rglob('*'):
            if path.is_file() and not path.is_symlink() and 's2l' not in path.name:
                if b'sing-box' in path.read_bytes():
                    return True
    return any(Path(path).exists() for path in ('/etc/sing-box', '/usr/local/etc/sing-box'))


def cleanup(message=lambda en, hant, hans: en):
    value = read()
    if value is None:
        print(message('Dependencies retained: this older installation has no ownership receipt.', '保留相依套件：此舊版安裝沒有歸屬記錄。', '保留依赖：此旧版安装没有归属记录。'))
        return
    candidates, retained = package_plan(value)
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
        if re.fullmatch(r'[0-9a-f]{64}', expected) and not SINGBOX.is_symlink() and fingerprint(SINGBOX) == expected and not singbox_shared():
            SINGBOX.unlink()
            print(message('Removed dedicated sing-box binary.', '已刪除專用 sing-box 程式。', '已删除专用 sing-box 程序。'))
        else:
            print(message('Retained shared or changed sing-box binary.', '保留共用或已修改的 sing-box 程式。', '保留共享或已修改的 sing-box 程序。'))


if __name__ == '__main__':
    if os.geteuid() != 0:
        raise SystemExit('requires root')
    os.umask(0o077)
    {'begin': begin, 'record': record, 'singbox': record_singbox, 'cleanup': cleanup}[sys.argv[1]]()

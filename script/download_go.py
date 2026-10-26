#!/usr/bin/env python3
"""Install a current stable Go toolchain only when no toolchain exists."""
from installer_i18n import tr
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
from urllib.request import urlopen
from install_preflight import go_version


def validate_member(member):
    if (member.name not in ('go', 'go/') and not member.name.startswith('go/')) or '..' in Path(member.name).parts or not (member.isdir() or member.isfile()):
        raise RuntimeError(tr('unexpected Go archive entry'))
    if member.name in ('go', 'go/') and not member.isdir():
        raise RuntimeError(tr('Go archive root must be a directory'))


def install(arch):
    destination = Path('/usr/local/go')
    if destination.exists() or destination.is_symlink():
        raise RuntimeError(tr('/usr/local/go already exists; refusing to overwrite'))
    with urlopen('https://go.dev/dl/?mode=json', timeout=20) as response:
        releases = json.load(response)
    for release in releases:
        if not release.get('stable'):
            continue
        go_version(release['version'])
        files = [f for f in release['files'] if f.get('os') == 'linux' and f.get('arch') == arch and f.get('kind') == 'archive']
        if files:
            asset = files[0]
            break
    else:
        raise RuntimeError(tr('no supported stable Go archive available'))
    if not re.fullmatch(r'go1\.\d+\.\d+\.linux-(amd64|arm64)\.tar\.gz', asset['filename']) or not re.fullmatch('[a-f0-9]{64}', asset['sha256']):
        raise RuntimeError(tr('invalid Go release metadata'))
    with tempfile.TemporaryDirectory(prefix='.s2l-go-', dir='/usr/local') as temporary:
        archive = Path(temporary) / 'go.tgz'
        # Avoid stalled HTTP/2 downloads through cloud NAT/proxies. A transfer
        # making no useful progress should enter curl's bounded retry promptly.
        subprocess.run(['curl', '-fSL', '--http1.1', '--retry', '2', '--connect-timeout', '15',
                        '--speed-limit', '1024', '--speed-time', '30', '--max-time', '180',
                        'https://go.dev/dl/' + asset['filename'], '-o', str(archive)], check=True, timeout=600)
        digest = hashlib.sha256()
        with archive.open('rb') as content:
            for chunk in iter(lambda: content.read(1048576), b''):
                digest.update(chunk)
        if digest.hexdigest() != asset['sha256']:
            raise RuntimeError(tr('Go archive checksum mismatch'))
        with tarfile.open(archive) as bundle:
            for member in bundle.getmembers():
                validate_member(member)
            bundle.extractall(temporary)
        Path(temporary, 'go').rename(destination)


if __name__ == '__main__':
    try:
        install(sys.argv[1])
    except Exception as error:
        raise SystemExit(str(error))

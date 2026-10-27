"""Uninstall/reinstall checks for the disposable full-system fixture only."""


def verify(guest, install, dial, retained=(), reinstall=True):
    guest('test "$(cat /etc/s2l-fresh-vm)" = disposable-qemu-s2l-fixture')
    # Prove ownership checks fail before downtime; do not delete user source.
    guest('touch /opt/src/s2l/keep-my-notes.txt')
    rejected = guest('s2l uninstall --dry-run', check=False)
    guest('test -f /opt/src/s2l/keep-my-notes.txt; rm /opt/src/s2l/keep-my-notes.txt')
    if rejected.returncode == 0:
        raise RuntimeError('uninstall accepted modified source')
    guest('''cp /etc/ppp/chap-secrets /etc/ppp/chap-secrets.s2l.tmp
cp /etc/xl2tpd/xl2tpd.conf /etc/xl2tpd/xl2tpd.conf.s2l.tmp
cp /etc/ppp/options.xl2tpd /etc/ppp/options.xl2tpd.s2l.tmp
cp /etc/ppp/options.xl2tpd.s2l-up /etc/ppp/.ppp-hook-fixture''')
    identity = 'sha256sum /opt/s2l/config.json /etc/ssh/sshd_config; systemctl show -p MainPID --value s2l xl2tpd nginx; cat /proc/sys/kernel/random/boot_id'
    before = guest(identity).stdout
    guest('s2l uninstall --dry-run')
    guest("printf 'cancel\\n' | s2l 13")
    if guest(identity).stdout != before:
        raise RuntimeError('uninstall preview/cancel changed live installation')
    guest('test -f /etc/ppp/chap-secrets.s2l.tmp; test -f /etc/ppp/.ppp-hook-fixture')
    print('PASS uninstall preview and cancellation preserve live installation', flush=True)
    guest('ip link add pppfixture type dummy; ip link set pppfixture up; ip route add 192.0.2.238/32 dev pppfixture')
    rejected = guest('env SSH_CONNECTION="192.0.2.238 40000 198.51.100.1 22" s2l uninstall --dry-run', check=False)
    guest('ip link del pppfixture')
    if rejected.returncode == 0 or guest(identity).stdout != before:
        raise RuntimeError('uninstall did not refuse SSH through PPP')
    guest('''iptables -S S2L_FIXTURE_FOREIGN >/dev/null 2>&1 || iptables -N S2L_FIXTURE_FOREIGN
iptables -C INPUT -s 192.0.2.239/32 -j S2L_FIXTURE_FOREIGN 2>/dev/null || iptables -A INPUT -s 192.0.2.239/32 -j S2L_FIXTURE_FOREIGN
printf '%s\\n' 'server { listen 38444; location / { return 200 "foreign-site"; } }' > /etc/nginx/conf.d/fixture-foreign.conf
nginx -t
systemctl reload nginx
cp /etc/ssh/sshd_config /tmp/ssh-uninstall-before''')
    guest('cp /var/lib/s2l-dependencies/receipt.json /tmp/dependencies-before-uninstall.json')
    guest('curl --fail --retry 3 --retry-connrefused http://127.0.0.1:38444/')
    guest("printf '#!/bin/sh\\nexit 0\\n' > /usr/local/bin/pgrep; chmod 755 /usr/local/bin/pgrep")
    failed = guest("printf 'UNINSTALL\\n' | s2l uninstall", check=False)
    guest('rm /usr/local/bin/pgrep; test -f /opt/s2l/config.json; test -f /usr/local/bin/s2l; iptables -S S2L_FORWARD; ip6tables -C FORWARD -i ppp+ -j DROP')
    if failed.returncode == 0:
        raise RuntimeError('uninstall ignored remaining PPP processes')
    guest('''python3 - <<'PY'
from pathlib import Path
import tarfile
for path in Path('/var/backups').glob('s2l-uninstall-*'):
 assert path.stat().st_mode & 0o777 == 0o700
 archive=path/'backup.tar.gz'
 assert archive.stat().st_mode & 0o777 == 0o600
 with tarfile.open(archive) as bundle:
  assert 'opt/s2l/tls/self-signed.key' in bundle.getnames()
PY''')
    print('PASS failed uninstall retains application, backups and leak guards', flush=True)
    guest('''mkdir -p /root/s2l-backup.fixture1/system
cp -a /opt/s2l /root/s2l-backup.fixture1/app
touch /root/s2l-backup.fixture1/system-paths.txt /root/s2l-backup.fixture1/iptables.v4 /root/s2l-backup.fixture1/iptables.v6 /root/s2l-backup.fixture1/requested-ref.txt
ip netns add s2l-proof-2147483647-1234567890''')
    result = guest("printf 'UNINSTALL\\n' | s2l 13", timeout=300)
    print(result.stdout, flush=True)
    guest('test ! -e /opt/s2l; test ! -e /usr/local/bin/s2l; test ! -e /etc/nginx/conf.d/s2l.conf; test ! -e /etc/systemd/system/s2l.service; test ! -e /etc/systemd/system/xl2tpd.service.d/s2l.conf; test ! -e /etc/sysctl.d/99-s2l.conf; ! pgrep -x pppd; ! systemctl is-active --quiet xl2tpd; ! systemctl is-active --quiet s2l; cmp /tmp/ssh-uninstall-before /etc/ssh/sshd_config; iptables -C INPUT -s 192.0.2.239/32 -j S2L_FIXTURE_FOREIGN; curl --fail http://127.0.0.1:38444/')
    import json
    guest('python3 - ' + "'" + json.dumps(list(retained)) + "'" + ''' <<'PY'
from pathlib import Path
import subprocess,json,sys
retained=set(json.loads(sys.argv[1]))
for binary in ('iptables-save','ip6tables-save'):
 text=subprocess.check_output([binary],text=True)
 assert all(name not in text for name in ('S2L_FORWARD','S2L_RETURN','S2L_MANAGEMENT','S2L-IPSEC'))
 assert 's2l:' not in text and 's2l-ipsec-nat' not in text
assert not list(Path('/var/backups').glob('s2l-uninstall-*'))
assert not list(Path('/var/backups').glob('s2l-kernel-*'))
assert not list(Path('/root').glob('s2l-backup.*'))
for name in ('/opt/src/s2l','/run/s2l','/run/s2l-ipsec-nat','/var/lib/s2l','/var/lib/s2l-install','/var/lib/s2l-dependencies'):
 assert not Path(name).exists(), name
assert Path('/usr/local/bin/sing-box').exists() == ('sing-box' in retained)
if 'ppp' in retained:
 assert Path('/etc/ppp/peers/fixture-shared').read_text() == 'noauth\\n'
if 'sing-box' in retained:
 assert Path('/etc/systemd/system/fixture-shared-singbox.service').is_file()
for name in ('/etc/ppp/chap-secrets.s2l.tmp','/etc/xl2tpd/xl2tpd.conf.s2l.tmp','/etc/ppp/options.xl2tpd.s2l.tmp','/etc/ppp/.ppp-hook-fixture'):
 assert not Path(name).exists(), name
for name in ('bootstrap','install','update','kernel'):
 assert not Path('/run/s2l-'+name+'.lock').exists()
for path, marker in (('/etc/ppp/chap-secrets','# s2l:'),('/etc/ipsec.secrets','include /opt/s2l/ipsec.secrets')):
 assert not Path(path).exists() or marker not in Path(path).read_text()
assert 's2l-proof-' not in subprocess.check_output(['ip','netns','list'],text=True)
for name in json.loads(Path('/tmp/dependencies-before-uninstall.json').read_text())['packages']:
 result=subprocess.run(['dpkg-query','-W','-f=${Status}',name],capture_output=True,text=True)
 if name in retained:
  assert result.stdout == 'install ok installed', name
 else:
  assert result.stdout != 'install ok installed' and 'config-files' not in result.stdout, name+': '+result.stdout
PY''')
    print('PASS uninstall removes managed resources and exclusive dependencies; preserves SSH, foreign firewall/Nginx and shared dependencies: ' + ','.join(retained), flush=True)
    if reinstall:
        guest(install, timeout=900)
        result = guest(dial, timeout=270)
        print(result.stdout, flush=True)
        print('PASS fresh reinstall and real PPP after uninstall', flush=True)


def verify_legacy(guest, install, dial, language):
    # Requires the real v1.3.12 archive, prepared by the host fixture. Test both
    # shared and exclusive dependencies starting from its unmodified installer.
    import shlex
    guest('test "$(cat /etc/s2l-fresh-vm)" = disposable-qemu-s2l-fixture; git clone /root/bootstrap-upstream.git /root/reviewed-update')
    old_install = '''mkdir -p /opt/src/s2l
tar -xzf /root/legacy-v1.3.12.tgz --strip-components=1 -C /opt/src/s2l
cd /opt/src/s2l
git init -b fixture-old
git add .
git -c user.name=Fixture -c user.email=fixture@example.invalid commit -m legacy-fixture
env ADMIN_USER=fixture ADMIN_PASS=fixture-test-password HTTPS_PORT=38443 S2L_VERSION=v1.3.12 S2L_COMMIT=2c7f6dab447f975562a21ab3235cb12ce2c60967 bash script/install.sh --lang ''' + shlex.quote(language)
    for shared in (True, False):
        result = guest(old_install, timeout=900)
        print(result.stdout[-1800:], flush=True)
        guest('test ! -e /var/lib/s2l-dependencies/receipt.json; test ! -e /opt/s2l/uninstall.py')
        print(guest(dial, timeout=270).stdout, flush=True)
        identity = 'sha256sum /opt/s2l/config.json /opt/s2l/tls/self-signed.key /opt/s2l/tls/self-signed.crt /etc/ssh/sshd_config'
        before = guest(identity).stdout
        # Include shared configuration BEFORE upgrade: migration must never
        # mistake it for a trustworthy baseline merely because it existed then.
        if shared:
            guest('''mkdir -p /etc/ppp/peers
printf 'noauth\\n' > /etc/ppp/peers/fixture-shared
printf '[Unit]\\nDescription=Shared fixture\\n[Service]\\nExecStart=/usr/local/bin/sing-box version\\n' > /etc/systemd/system/fixture-shared-singbox.service
systemctl daemon-reload''')
        result = guest('bash /root/reviewed-update/script/update.sh', timeout=900)
        print(result.stdout[-1800:], flush=True)
        if guest(identity).stdout != before:
            raise RuntimeError('legacy upgrade changed credentials, certificate or SSH')
        guest('''python3 - <<'PY'
from pathlib import Path
import json
r=json.loads(Path('/var/lib/s2l-dependencies/receipt.json').read_text())
assert 'ppp' in r['legacy_packages'] and 'xl2tpd' in r['legacy_packages']
assert r['legacy_singbox'] and len(r['singbox_sha256'])==64
assert '/etc/ppp/peers/fixture-shared' not in r['config_paths']
PY''')
        print(guest(dial, timeout=270).stdout, flush=True)
        verify(guest, install, dial, retained=('ppp', 'sing-box') if shared else (), reinstall=not shared)
        if shared:
            guest('rm /etc/ppp/peers/fixture-shared /etc/systemd/system/fixture-shared-singbox.service; systemctl daemon-reload')
        print('PASS real v1.3.12 upgrade and uninstall (' + ('shared dependencies retained' if shared else 'dedicated dependencies removed') + ')', flush=True)

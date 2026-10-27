"""Uninstall/reinstall checks for the disposable full-system fixture only."""


def verify(guest, install, dial):
    guest('test "$(cat /etc/s2l-fresh-vm)" = disposable-qemu-s2l-fixture')
    # Prove ownership checks fail before downtime; do not delete user source.
    guest('touch /opt/src/s2l/keep-my-notes.txt')
    rejected = guest('s2l uninstall --dry-run', check=False)
    guest('test -f /opt/src/s2l/keep-my-notes.txt; rm /opt/src/s2l/keep-my-notes.txt')
    if rejected.returncode == 0:
        raise RuntimeError('uninstall accepted modified source')
    identity = 'sha256sum /opt/s2l/config.json /etc/ssh/sshd_config; systemctl show -p MainPID --value s2l xl2tpd nginx; cat /proc/sys/kernel/random/boot_id'
    before = guest(identity).stdout
    guest('s2l uninstall --dry-run')
    guest("printf 'cancel\\n' | s2l 13")
    if guest(identity).stdout != before:
        raise RuntimeError('uninstall preview/cancel changed live installation')
    print('PASS uninstall preview and cancellation preserve live installation', flush=True)
    guest('ip link add pppfixture type dummy; ip link set pppfixture up; ip route add 192.0.2.238/32 dev pppfixture')
    rejected = guest('env SSH_CONNECTION="192.0.2.238 40000 198.51.100.1 22" s2l uninstall --dry-run', check=False)
    guest('ip link del pppfixture')
    if rejected.returncode == 0 or guest(identity).stdout != before:
        raise RuntimeError('uninstall did not refuse SSH through PPP')
    guest('''iptables -N S2L_FIXTURE_FOREIGN
iptables -A INPUT -s 192.0.2.239/32 -j S2L_FIXTURE_FOREIGN
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
    guest('''python3 - <<'PY'
from pathlib import Path
import subprocess,json
for binary in ('iptables-save','ip6tables-save'):
 text=subprocess.check_output([binary],text=True)
 assert all(name not in text for name in ('S2L_FORWARD','S2L_RETURN','S2L_MANAGEMENT','S2L-IPSEC'))
 assert 's2l:' not in text and 's2l-ipsec-nat' not in text
assert not list(Path('/var/backups').glob('s2l-uninstall-*'))
assert not list(Path('/var/backups').glob('s2l-kernel-*'))
assert not list(Path('/root').glob('s2l-backup.*'))
for name in ('/opt/src/s2l','/run/s2l','/run/s2l-ipsec-nat','/var/lib/s2l','/var/lib/s2l-install','/var/lib/s2l-dependencies','/usr/local/bin/sing-box'):
 assert not Path(name).exists(), name
for name in ('bootstrap','install','update','kernel'):
 assert not Path('/run/s2l-'+name+'.lock').exists()
for path, marker in (('/etc/ppp/chap-secrets','# s2l:'),('/etc/ipsec.secrets','include /opt/s2l/ipsec.secrets')):
 assert not Path(path).exists() or marker not in Path(path).read_text()
assert 's2l-proof-' not in subprocess.check_output(['ip','netns','list'],text=True)
for name in json.loads(Path('/tmp/dependencies-before-uninstall.json').read_text())['packages']:
 result=subprocess.run(['dpkg-query','-W','-f=${Status}',name],capture_output=True,text=True)
 assert result.stdout != 'install ok installed' and 'config-files' not in result.stdout, name+': '+result.stdout
PY''')
    print('PASS uninstall removes source, backups, runtime data and dedicated sing-box; preserves SSH and foreign firewall/Nginx', flush=True)
    guest(install, timeout=900)
    result = guest(dial, timeout=270)
    print(result.stdout, flush=True)
    print('PASS fresh reinstall and real PPP after uninstall', flush=True)

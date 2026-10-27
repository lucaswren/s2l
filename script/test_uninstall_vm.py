"""Uninstall/reinstall checks for the disposable full-system fixture only."""


def verify(guest, install, dial):
    guest('test "$(cat /etc/s2l-fresh-vm)" = disposable-qemu-s2l-fixture')
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
    guest('curl --fail --retry 3 --retry-connrefused http://127.0.0.1:38444/')
    guest("printf '#!/bin/sh\\nexit 0\\n' > /usr/local/bin/pgrep; chmod 755 /usr/local/bin/pgrep")
    failed = guest("printf 'UNINSTALL\\n' | s2l uninstall", check=False)
    guest('rm /usr/local/bin/pgrep; test -f /opt/s2l/config.json; test -f /usr/local/bin/s2l; iptables -S S2L_FORWARD; ip6tables -C FORWARD -i ppp+ -j DROP')
    if failed.returncode == 0:
        raise RuntimeError('uninstall ignored remaining PPP processes')
    print('PASS failed uninstall retains application, backups and leak guards', flush=True)
    result = guest("printf 'UNINSTALL\\n' | s2l 13", timeout=180)
    print(result.stdout, flush=True)
    guest('test ! -e /opt/s2l; test ! -e /usr/local/bin/s2l; test ! -e /etc/nginx/conf.d/s2l.conf; test ! -e /etc/systemd/system/s2l.service; test ! -e /etc/systemd/system/xl2tpd.service.d/s2l.conf; test ! -e /etc/sysctl.d/99-s2l.conf; ! pgrep -x pppd; ! systemctl is-active --quiet xl2tpd; ! systemctl is-active --quiet s2l; cmp /tmp/ssh-uninstall-before /etc/ssh/sshd_config; iptables -C INPUT -s 192.0.2.239/32 -j S2L_FIXTURE_FOREIGN; curl --fail http://127.0.0.1:38444/')
    guest('''python3 - <<'PY'
from pathlib import Path
import subprocess,tarfile
for binary in ('iptables-save','ip6tables-save'):
 text=subprocess.check_output([binary],text=True)
 assert all(name not in text for name in ('S2L_FORWARD','S2L_RETURN','S2L_MANAGEMENT','S2L-IPSEC'))
 assert 's2l:' not in text and 's2l-ipsec-nat' not in text
backups=list(Path('/var/backups').glob('s2l-uninstall-*'))
assert backups
for path in backups:
 assert path.stat().st_mode & 0o777 == 0o700
 archive=path/'backup.tar.gz'
 assert archive.stat().st_mode & 0o777 == 0o600
 with tarfile.open(archive) as bundle:
  names=bundle.getnames()
  assert all(name in names for name in ('opt/s2l/config.json','opt/s2l/data/state.json','opt/s2l/tls/self-signed.key','etc/ppp/chap-secrets'))
assert '# s2l:' not in Path('/etc/ppp/chap-secrets').read_text()
assert 'include /opt/s2l/ipsec.secrets' not in Path('/etc/ipsec.secrets').read_text()
PY''')
    print('PASS uninstall preserves SSH, foreign firewall/Nginx and private backups', flush=True)
    guest(install, timeout=900)
    result = guest(dial, timeout=270)
    print(result.stdout, flush=True)
    print('PASS fresh reinstall and real PPP after uninstall', flush=True)

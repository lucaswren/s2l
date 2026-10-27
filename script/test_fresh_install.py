#!/usr/bin/env python3
"""Boot a disposable Ubuntu/Debian VM, install, inject failures, reboot and dial.
Only the QEMU guest is modified. The host needs QEMU, cloud-localds and SSH.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import socket
import subprocess
import tarfile
import tempfile
import time


def run(args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def free_port():
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        return listener.getsockname()[1]


def wait_for_guest(vm, guest, console, previous=None, timeout=240):
    """Retry transient SSH failures during boot, within one total deadline."""
    end = time.monotonic() + timeout
    last = 'SSH not ready'
    while time.monotonic() < end:
        if vm.poll() is not None:
            raise RuntimeError('QEMU exited: ' + console.read_text(errors='replace')[-6000:])
        remaining = end - time.monotonic()
        if remaining <= 0:
            break
        try:
            result = guest('cat /proc/sys/kernel/random/boot_id', check=False, timeout=min(15, remaining))
        except subprocess.TimeoutExpired:
            last = 'SSH readiness probe timed out'
        else:
            boot = result.stdout.strip()
            if result.returncode == 0 and boot and boot != previous:
                return boot
            last = 'waiting for new boot ID' if result.returncode == 0 else 'SSH not ready'
        time.sleep(min(3, max(0, end - time.monotonic())))
    raise RuntimeError('guest boot timed out: ' + last)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--distro', choices=('ubuntu-24.04', 'debian-12'), default='ubuntu-24.04')
    args = parser.parse_args()
    debian = args.distro == 'debian-12'
    repository = Path(__file__).resolve().parents[1]
    for name in ('qemu-system-x86_64', 'qemu-img', 'cloud-localds', 'ssh', 'scp', 'ssh-keygen', 'curl'):
        if not shutil.which(name):
            raise SystemExit('missing VM fixture dependency: ' + name)
    image_url = ('https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-generic-amd64.qcow2' if debian else
                 'https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img')
    checksum = 'sha512' if debian else 'sha256'
    with tempfile.TemporaryDirectory(prefix='s2l-fresh-vm-') as temporary:
        root = Path(temporary)
        image = root / 'base.img'
        run(['curl', '-fSL', '--retry', '2', '--connect-timeout', '15', '--max-time', '600', image_url, '-o', str(image)])
        sums = subprocess.check_output(['curl', '-fsSL', '--retry', '2', '--connect-timeout', '15', '--max-time', '60',
                                        image_url.rsplit('/', 1)[0] + '/' + checksum.upper() + 'SUMS'], text=True)
        expected = [line.split()[0] for line in sums.splitlines() if line.split()[-1].lstrip('*') == image_url.rsplit('/', 1)[-1]]
        digest = hashlib.new(checksum)
        with image.open('rb') as source:
            for block in iter(lambda: source.read(1048576), b''):
                digest.update(block)
        if len(expected) != 1 or digest.hexdigest() != expected[0]:
            raise RuntimeError(args.distro + ' image checksum mismatch')
        disk = root / 'system.qcow2'
        run(['qemu-img', 'create', '-f', 'qcow2', '-F', 'qcow2', '-b', str(image), str(disk), '18G'])
        key = root / 'identity'
        run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(key)])
        public_key = key.with_suffix('.pub').read_text().strip()
        user_data = root / 'user-data'
        user_data.write_text('#cloud-config\nusers:\n  - name: fixture\n    groups: sudo\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    shell: /bin/bash\n    lock_passwd: true\n    ssh_authorized_keys:\n      - ' + public_key + '\n')
        metadata = root / 'meta-data'
        metadata.write_text('instance-id: s2l-fresh-fixture\nlocal-hostname: s2l-fresh-fixture\n')
        seed = root / 'seed.img'
        run(['cloud-localds', str(seed), str(user_data), str(metadata)])
        port = free_port()
        log = (root / 'console.log').open('wb')
        command = ['qemu-system-x86_64', '-machine', 'q35,accel=kvm:tcg', '-cpu', 'max', '-smp', '2', '-m', '3072',
                   '-display', 'none', '-monitor', 'none', '-serial', 'stdio',
                   '-drive', 'file=' + str(disk) + ',if=virtio,format=qcow2',
                   '-drive', 'file=' + str(seed) + ',if=virtio,format=raw',
                   '-nic', f'user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:{port}-:22']
        vm = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
        ssh_base = ['ssh', '-i', str(key), '-p', str(port), '-o', 'StrictHostKeyChecking=no',
                    '-o', 'UserKnownHostsFile=/dev/null', '-o', 'ConnectTimeout=5', '-o', 'LogLevel=ERROR', 'fixture@127.0.0.1']
        def guest(command, check=True, timeout=900):
            return subprocess.run(ssh_base + ['sudo -n bash -lc ' + shlex.quote('set -euo pipefail; ' + command)],
                                  check=check, text=True, capture_output=True, timeout=timeout)
        def copy(path, target):
            run(['scp', '-q', '-i', str(key), '-P', str(port), '-o', 'StrictHostKeyChecking=no',
                 '-o', 'UserKnownHostsFile=/dev/null', str(path), 'fixture@127.0.0.1:' + target])
        try:
            boot = wait_for_guest(vm, guest, root / 'console.log')
            guest('cloud-init status --wait', timeout=180)
            source_archive = root / 'source.tgz'
            with tarfile.open(source_archive, 'w:gz') as bundle:
                for name in ('cmd', 'internal', 'deploy', 'script', 'web', 'go.mod'):
                    bundle.add(repository / name, arcname=name, filter=lambda entry: None if any(part in entry.name.split('/') for part in ('__pycache__', 'node_modules', '.git')) else entry)
            copy(source_archive, '/tmp/source.tgz')
            guest("mkdir -p /opt/src/s2l; tar -xzf /tmp/source.tgz -C /opt/src/s2l; test ! -e /usr/local/go; ! command -v go; printf '%s\\n' disposable-qemu-s2l-fixture > /etc/s2l-fresh-vm")
            # Exercise different languages end-to-end, including HTTPS children.
            language = 'en' if debian else 'zh-Hant'
            install = 'cd /opt/src/s2l && env ADMIN_USER=fixture ADMIN_PASS=fixture-test-password HTTPS_PORT=38443 bash script/install.sh --lang ' + language + ' 2>&1 | tee /tmp/install-last.log'
            # Exercise production kernel preparation before PPP can run on a
            # regressed Noble image. Only this disposable fixture reboots itself.
            prepared = guest('python3 /opt/src/s2l/script/manage_kernel.py', check=False, timeout=3800)
            if prepared.returncode == 20:
                print(prepared.stdout[-5000:], flush=True)
                guest('test ! -e /opt/s2l/config.json; test ! -e /var/lib/s2l-install/journal.json; test "$(cat /proc/sys/kernel/random/boot_id)" = ' + shlex.quote(boot))
                repeated = guest(install, check=False, timeout=120)
                if repeated.returncode != 20 or '[INFO] 從系統 APT 來源準備' in repeated.stdout:
                    raise RuntimeError('prepared kernel was not reused before reboot: ' + repeated.stdout + repeated.stderr)
                guest('test ! -e /opt/s2l/config.json; test ! -e /var/lib/s2l-install/journal.json')
                guest('systemctl reboot', check=False, timeout=20)
                boot = wait_for_guest(vm, guest, root / 'console.log', previous=boot)
                guest('python3 /opt/src/s2l/script/manage_kernel.py')
                print('PASS kernel preparation, exit 20, reuse and deliberate reboot', flush=True)
            elif prepared.returncode != 0:
                raise RuntimeError('kernel preparation failed: ' + prepared.stdout + prepared.stderr)
            else:
                print('PASS image kernel does not require known-regression preparation', flush=True)
            print('Guest kernel: ' + guest('uname -r').stdout.strip(), flush=True)
            # Existing incompatible sing-box must be rejected without application writes.
            guest("printf '#!/bin/sh\\necho sing-box version 1.12.0\\n' > /usr/local/bin/sing-box; chmod 755 /usr/local/bin/sing-box")
            failed = guest(install, check=False)
            print('Cold dependency/toolchain timings:\n' + '\n'.join(line for line in failed.stdout.splitlines() if '[TIME]' in line), flush=True)
            if failed.returncode == 0:
                raise RuntimeError('incompatible sing-box was accepted: ' + failed.stdout + failed.stderr)
            if '1.11.15' not in failed.stdout + failed.stderr:
                raise RuntimeError('installation failed before the incompatible sing-box check: ' + failed.stdout + failed.stderr)
            guest('test ! -e /opt/s2l/config.json; test -x /usr/local/go/bin/go; rm /usr/local/bin/sing-box')
            print('PASS incompatible dependency rejection', flush=True)
            # A service start failure must restore files, sysctl and firewall state.
            wrapper = root / 'systemctl'
            wrapper.write_text('#!/bin/bash\nif [[ "$1" == restart && "${2:-}" == s2l ]]; then\n if [[ -f /tmp/fail-s2l-install ]]; then echo fail > /tmp/install-injected; exit 71; fi\n if [[ -f /tmp/kill-s2l-install ]]; then echo kill > /tmp/install-injected; kill -KILL "$PPID"; exit 72; fi\nfi\nif [[ "$1" == reload && "${2:-}" == nginx && -f /tmp/fail-s2l-https ]]; then echo https > /tmp/install-injected; exit 73; fi\nexec /usr/bin/systemctl "$@"\n')
            copy(wrapper, '/tmp/systemctl-wrapper')
            guest('install -m 755 /tmp/systemctl-wrapper /usr/local/bin/systemctl; touch /tmp/fail-s2l-install; cp -p /etc/ppp/chap-secrets /tmp/chap-before; sysctl -n net.ipv4.ip_forward > /tmp/forward-before')
            failed = guest(install, check=False)
            print('Cold build timings:\n' + '\n'.join(line for line in failed.stdout.splitlines() if '[TIME]' in line), flush=True)
            if failed.returncode == 0:
                raise RuntimeError('injected install failure was ignored')
            result = guest('test "$(cat /tmp/install-injected)" = fail; rm /tmp/install-injected; test ! -e /opt/s2l/config.json; cmp /tmp/chap-before /etc/ppp/chap-secrets; test "$(sysctl -n net.ipv4.ip_forward)" = "$(cat /tmp/forward-before)"; test ! -e /var/lib/s2l-install/journal.json; rm /tmp/fail-s2l-install')
            print('PASS failed installation rollback', flush=True)
            guest('! iptables -S S2L_FORWARD; ! iptables -S S2L_RETURN; ! iptables -S S2L_MANAGEMENT')
            guest('touch /tmp/fail-s2l-https')
            failed = guest(install, check=False)
            if failed.returncode == 0:
                raise RuntimeError('injected HTTPS failure was ignored')
            guest('test "$(cat /tmp/install-injected)" = https; test ! -e /opt/s2l/config.json; test ! -e /etc/ppp/options.xl2tpd.s2l-up; cmp /tmp/chap-before /etc/ppp/chap-secrets; rm /tmp/fail-s2l-https /tmp/install-injected')
            print('PASS HTTPS failure rollback after service startup', flush=True)
            guest('! iptables -S S2L_FORWARD; ! iptables -S S2L_RETURN; ! iptables -S S2L_MANAGEMENT')
            guest('touch /tmp/kill-s2l-install')
            failed = guest(install, check=False)
            if failed.returncode == 0:
                raise RuntimeError('hard interruption was ignored')
            guest('test "$(cat /tmp/install-injected)" = kill; test -f /var/lib/s2l-install/journal.json; rm /tmp/kill-s2l-install /tmp/install-injected')
            # Resume through the public one-command entrypoint with an active
            # xl2tpd and an incomplete journal, using a disposable local origin.
            guest("apt-get -o DPkg::Lock::Timeout=120 install -y --no-install-recommends git; cd /opt/src/s2l; git init -b fixture; printf '%s\\n' bin/ __pycache__/ '*.pyc' > .git/info/exclude; git add .; git -c user.name=Fixture -c user.email=fixture@example.invalid commit -m fixture; git clone --bare . /root/bootstrap-upstream.git; git remote add origin /root/bootstrap-upstream.git; cp script/remote_install.sh /root/fixture-remote-install.sh; systemctl is-active --quiet xl2tpd", timeout=180)
            remote_install = 'cd / && env REPO_URL=/root/bootstrap-upstream.git BRANCH=fixture SRC_DIR=/opt/src/s2l ADMIN_USER=fixture ADMIN_PASS=fixture-test-password HTTPS_PORT=38443 bash /root/fixture-remote-install.sh --lang ' + language + ' 2>&1 | tee /tmp/install-last.log'
            result = guest(remote_install)
            print(result.stdout[-5000:], flush=True)
            expected = ('S2L installation complete', 'Nginx / HTTPS verification') if debian else ('S2L 安裝完成', 'Nginx／HTTPS 驗證')
            if not all(message in result.stdout for message in expected):
                raise RuntimeError('installer language was not propagated through HTTPS setup')
            if 'will validate recovery' not in result.stdout and '會先驗證復原條件' not in result.stdout:
                raise RuntimeError('bootstrap did not reach interrupted-install recovery')
            print('PASS one-command crash recovery and full fresh installation', flush=True)
            guest('rm /usr/local/bin/systemctl; systemctl is-active --quiet s2l xl2tpd nginx s2l-guard; test ! -e /var/lib/s2l-install/journal.json')
            guest('cmp /opt/src/s2l/script/manage_kernel.py /opt/s2l/manage_kernel.py; s2l --help | grep -q kernel-upgrade')
            # Exercise the installed helper under Ubuntu's enforcing AppArmor
            # profile. A loaded connection alone does not prove PSK readability.
            guest("printf '%s\\n' '{\"enabled\":true,\"psk\":\"fixture-ipsec-secret\"}' | python3 /opt/s2l/manage_ipsec.py apply")
            guest('ipsec rereadsecrets')
            guest("python3 - " + ("debian" if debian else "ubuntu") + " <<'PY'\nfrom pathlib import Path\nimport subprocess,time,sys\npid=Path('/run/charon.pid').read_text().strip()\nprofile=Path('/proc')/pid/'attr/current'\nif sys.argv[1] == 'ubuntu': assert '/usr/lib/ipsec/charon (enforce)' in profile.read_text(), 'charon is not confined'\nfor _ in range(20):\n log=subprocess.check_output(['journalctl','-u','s2l-ipsec','--no-pager','-o','cat'],text=True)\n if \"loading secrets from '/opt/s2l/ipsec.secrets'\" in log and 'loaded IKE secret for %any' in log: break\n time.sleep(.25)\nelse: raise AssertionError('managed PSK was not loaded by confined charon')\nPY")
            guest("printf '%s\\n' '{\"enabled\":false}' | python3 /opt/s2l/manage_ipsec.py apply")
            print('PASS managed IPsec PSK loading' + ('' if debian else ' with AppArmor enforcement preserved'), flush=True)
            guest('cd /opt/src/s2l; /usr/local/go/bin/go test -c -o /root/s2l-fresh.test ./internal/service', timeout=300)
            dial = 'S2L_FRESH_VM_TEST=1 /root/s2l-fresh.test -test.run ^TestFreshInstalledServiceDial$ -test.v -test.timeout 240s'
            result = guest(dial, timeout=270)
            print(result.stdout, flush=True)
            original = guest('sha256sum /opt/s2l/config.json').stdout
            # Run the shipped maintenance command against an installed system.
            # Only this fixture's later reboot may change the boot ID or daemons.
            live_identity = 'cat /proc/sys/kernel/random/boot_id; systemctl show -p MainPID --value s2l xl2tpd nginx'
            before_kernel = guest(live_identity).stdout
            prepared = guest('s2l kernel-upgrade', check=not debian, timeout=3800)
            if debian and (prepared.returncode == 0 or '没有受支持' not in prepared.stderr):
                raise RuntimeError('Debian kernel upgrade did not refuse before mutation: ' + prepared.stdout + prepared.stderr)
            print(prepared.stdout[-3000:], flush=True)
            if guest(live_identity).stdout != before_kernel or guest('sha256sum /opt/s2l/config.json').stdout != original:
                raise RuntimeError('kernel maintenance restarted a service, rebooted or changed S2L configuration')
            print('PASS installed kernel-upgrade command preserves boot, service processes and configuration', flush=True)
            guest('systemctl reboot', check=False, timeout=20)
            wait_for_guest(vm, guest, root / 'console.log', previous=boot)
            guest('systemctl is-active --quiet s2l xl2tpd nginx s2l-guard; python3 /opt/src/s2l/script/verify_update.py health /opt/s2l/config.json', timeout=150)
            if guest('sha256sum /opt/s2l/config.json').stdout != original:
                raise RuntimeError('reboot changed installed configuration')
            result = guest(dial, timeout=270)
            print(result.stdout, flush=True)
            print('PASS full OS reboot, restored fixed mapping and repeat real PPP/fault tests', flush=True)
            from test_uninstall_vm import verify as verify_uninstall, verify_legacy
            verify_uninstall(guest, remote_install, dial, reinstall=False)
            legacy_archive = root / 'legacy-v1.3.12.tgz'
            run(['curl', '-fL', '--retry', '2', '--connect-timeout', '15', '--max-time', '180',
                 'https://codeload.github.com/lucaswren/s2l/tar.gz/2c7f6dab447f975562a21ab3235cb12ce2c60967', '-o', str(legacy_archive)])
            copy(legacy_archive, '/tmp/legacy-v1.3.12.tgz')
            guest('mv /tmp/legacy-v1.3.12.tgz /root/legacy-v1.3.12.tgz')
            verify_legacy(guest, remote_install, dial, language)
        except Exception as error:
            for name in ('stdout', 'stderr'):
                output = getattr(error, name, None)
                if output:
                    print(str(output)[-12000:], flush=True)
            try:
                print(guest('tail -n 100 /tmp/install-last.log 2>/dev/null || true; journalctl -u s2l -u xl2tpd -n 100 --no-pager; cat /var/lib/s2l-install/journal.json 2>/dev/null || true', check=False, timeout=20).stdout, flush=True)
            except Exception:
                pass
            print((root / 'console.log').read_text(errors='replace')[-5000:], flush=True)
            raise
        finally:
            vm.terminate()
            try:
                vm.wait(timeout=15)
            except subprocess.TimeoutExpired:
                vm.kill(); vm.wait()
            log.close()


if __name__ == '__main__':
    main()

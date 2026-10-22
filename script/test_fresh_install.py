#!/usr/bin/env python3
"""Boot a disposable Ubuntu VM, install S2L, inject failures, then reboot and dial.
Only the QEMU guest is modified. The host needs QEMU, cloud-localds and SSH.
"""
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


def main():
    repository = Path(__file__).resolve().parents[1]
    for name in ('qemu-system-x86_64', 'qemu-img', 'cloud-localds', 'ssh', 'scp', 'ssh-keygen', 'curl', 'go'):
        if not shutil.which(name):
            raise SystemExit('missing VM fixture dependency: ' + name)
    image_url = 'https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img'
    with tempfile.TemporaryDirectory(prefix='s2l-fresh-vm-') as temporary:
        root = Path(temporary)
        image = root / 'base.img'
        run(['curl', '-fSL', '--retry', '2', '--connect-timeout', '15', '--max-time', '600', image_url, '-o', str(image)])
        sums = subprocess.check_output(['curl', '-fsSL', '--retry', '2', '--connect-timeout', '15', '--max-time', '60',
                                        image_url.rsplit('/', 1)[0] + '/SHA256SUMS'], text=True)
        expected = [line.split()[0] for line in sums.splitlines() if line.split()[-1].lstrip('*') == image_url.rsplit('/', 1)[-1]]
        digest = hashlib.sha256()
        with image.open('rb') as source:
            for block in iter(lambda: source.read(1048576), b''):
                digest.update(block)
        if len(expected) != 1 or digest.hexdigest() != expected[0]:
            raise RuntimeError('Ubuntu image checksum mismatch')
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
        def ready(previous=None):
            end = time.monotonic() + 240
            while time.monotonic() < end:
                if vm.poll() is not None:
                    raise RuntimeError('QEMU exited: ' + (root / 'console.log').read_text(errors='replace')[-6000:])
                result = guest('cat /proc/sys/kernel/random/boot_id', check=False, timeout=15)
                if result.returncode == 0 and result.stdout.strip() and result.stdout.strip() != previous:
                    return result.stdout.strip()
                time.sleep(3)
            raise RuntimeError('guest boot timed out')
        def copy(path, target):
            run(['scp', '-q', '-i', str(key), '-P', str(port), '-o', 'StrictHostKeyChecking=no',
                 '-o', 'UserKnownHostsFile=/dev/null', str(path), 'fixture@127.0.0.1:' + target])
        try:
            boot = ready()
            guest('cloud-init status --wait', timeout=180)
            source_archive = root / 'source.tgz'
            with tarfile.open(source_archive, 'w:gz') as bundle:
                for name in ('cmd', 'internal', 'deploy', 'script', 'web/dist', 'go.mod'):
                    bundle.add(repository / name, arcname=name, filter=lambda entry: None if '__pycache__' in entry.name else entry)
            toolchain = Path(subprocess.check_output(['go', 'env', 'GOROOT'], text=True).strip())
            go_archive = root / 'go.tgz'
            with tarfile.open(go_archive, 'w:gz', compresslevel=1) as bundle:
                bundle.add(toolchain, arcname='go')
            copy(source_archive, '/tmp/source.tgz')
            copy(go_archive, '/tmp/go.tgz')
            guest("mkdir -p /opt/src/s2l; tar -xzf /tmp/source.tgz -C /opt/src/s2l; tar -xzf /tmp/go.tgz -C /usr/local; printf '%s\\n' disposable-qemu-s2l-fixture > /etc/s2l-fresh-vm")
            install = 'cd /opt/src/s2l && env ADMIN_USER=fixture ADMIN_PASS=fixture-test-password HTTPS_PORT=38443 bash script/install.sh'
            # Existing incompatible sing-box must be rejected without application writes.
            guest("printf '#!/bin/sh\\necho sing-box version 1.12.0\\n' > /usr/local/bin/sing-box; chmod 755 /usr/local/bin/sing-box")
            failed = guest(install, check=False)
            if failed.returncode == 0 or '1.11.15' not in failed.stdout + failed.stderr:
                raise RuntimeError('incompatible sing-box was accepted: ' + failed.stdout + failed.stderr)
            guest('test ! -e /opt/s2l/config.json; rm /usr/local/bin/sing-box')
            print('PASS incompatible dependency rejection', flush=True)
            # A service start failure must restore files, sysctl and firewall state.
            wrapper = root / 'systemctl'
            wrapper.write_text('#!/bin/bash\nif [[ "$1" == restart && "${2:-}" == s2l ]]; then\n if [[ -f /tmp/fail-s2l-install ]]; then echo fail > /tmp/install-injected; exit 71; fi\n if [[ -f /tmp/kill-s2l-install ]]; then echo kill > /tmp/install-injected; kill -KILL "$PPID"; exit 72; fi\nfi\nif [[ "$1" == reload && "${2:-}" == nginx && -f /tmp/fail-s2l-https ]]; then echo https > /tmp/install-injected; exit 73; fi\nexec /usr/bin/systemctl "$@"\n')
            copy(wrapper, '/tmp/systemctl-wrapper')
            guest('install -m 755 /tmp/systemctl-wrapper /usr/local/bin/systemctl; touch /tmp/fail-s2l-install; cp -p /etc/ppp/chap-secrets /tmp/chap-before; sysctl -n net.ipv4.ip_forward > /tmp/forward-before')
            failed = guest(install, check=False)
            if failed.returncode == 0:
                raise RuntimeError('injected install failure was ignored')
            result = guest('test "$(cat /tmp/install-injected)" = fail; rm /tmp/install-injected; test ! -e /opt/s2l/config.json; cmp /tmp/chap-before /etc/ppp/chap-secrets; test "$(sysctl -n net.ipv4.ip_forward)" = "$(cat /tmp/forward-before)"; test ! -e /var/lib/s2l-install/journal.json; rm /tmp/fail-s2l-install')
            print('PASS failed installation rollback', flush=True)
            guest('touch /tmp/fail-s2l-https')
            failed = guest(install, check=False)
            if failed.returncode == 0:
                raise RuntimeError('injected HTTPS failure was ignored')
            guest('test "$(cat /tmp/install-injected)" = https; test ! -e /opt/s2l/config.json; test ! -e /etc/ppp/options.xl2tpd.s2l-up; cmp /tmp/chap-before /etc/ppp/chap-secrets; rm /tmp/fail-s2l-https /tmp/install-injected')
            print('PASS HTTPS failure rollback after service startup', flush=True)
            guest('touch /tmp/kill-s2l-install')
            failed = guest(install, check=False)
            if failed.returncode == 0:
                raise RuntimeError('hard interruption was ignored')
            guest('test "$(cat /tmp/install-injected)" = kill; test -f /var/lib/s2l-install/journal.json; rm /tmp/kill-s2l-install /tmp/install-injected')
            result = guest(install)
            print(result.stdout[-5000:], flush=True)
            print('PASS crash recovery and full fresh installation', flush=True)
            guest('rm /usr/local/bin/systemctl; systemctl is-active --quiet s2l xl2tpd nginx s2l-guard; test ! -e /var/lib/s2l-install/journal.json')
            guest('cd /opt/src/s2l; /usr/local/go/bin/go test -c -o /tmp/s2l-fresh.test ./internal/service', timeout=300)
            dial = 'S2L_FRESH_VM_TEST=1 /tmp/s2l-fresh.test -test.run ^TestFreshInstalledServiceDial$ -test.v -test.timeout 240s'
            result = guest(dial, timeout=270)
            print(result.stdout, flush=True)
            original = guest('sha256sum /opt/s2l/config.json').stdout
            guest('systemctl reboot', check=False, timeout=20)
            ready(boot)
            guest('systemctl is-active --quiet s2l xl2tpd nginx s2l-guard', timeout=30)
            if guest('sha256sum /opt/s2l/config.json').stdout != original:
                raise RuntimeError('reboot changed installed configuration')
            result = guest(dial, timeout=270)
            print(result.stdout, flush=True)
            print('PASS full OS reboot, restored fixed mapping and repeat real PPP/fault tests', flush=True)
        except Exception as error:
            for name in ('stdout', 'stderr'):
                output = getattr(error, name, None)
                if output:
                    print(str(output)[-12000:], flush=True)
            try:
                print(guest('journalctl -u s2l -u xl2tpd -n 100 --no-pager; cat /var/lib/s2l-install/journal.json 2>/dev/null || true', check=False, timeout=20).stdout, flush=True)
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

#!/usr/bin/env python3
"""Prepare a kernel for fresh installation or explicit maintenance; never reboot.

Fresh installation upgrades only known regressed kernels and exits 20 until
rebooted. Explicit maintenance prepares Ubuntu 24.04 HWE and exits 0 when ready.
"""
from installer_i18n import tr
import argparse
from contextlib import ExitStack
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile

BOOT = Path('/boot')
GRUB_DEFAULTS = Path('/etc/default/grub')
OS_RELEASE = Path('/etc/os-release')
REBOOT_REQUIRED = 20
HWE_PACKAGE = 'linux-generic-hwe-24.04'
BACKUPS = Path('/var/backups')


def affected_kernel(distro, release):
    # Ubuntu bug 2169477: PPPoL2TP session refcount regression.
    return distro == 'ubuntu' and re.fullmatch(r'6\.8\.0-(146|147)-generic', release) is not None


def system(check_kernel=False):
    # This check only belongs to fresh installation. Installed maintenance needs
    # no installer modules and must not stop or recover an existing deployment.
    try:
        from install_preflight import system as preflight
    except ImportError as error:
        raise RuntimeError(tr('首次安装请使用完整源码中的 install.sh；已有主机使用 s2l kernel-upgrade')) from error
    preflight(check_kernel=check_kernel)


def require_root():
    if sys.platform != 'linux' or os.geteuid() != 0:
        raise RuntimeError(tr('需要 Linux root；请使用 sudo s2l kernel-upgrade'))


def maintenance_system():
    require_root()
    if not Path('/run/systemd/system').is_dir():
        raise RuntimeError(tr('需要以 systemd 运行的 Ubuntu 24.04 主机'))


def run(args, timeout=30):
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=timeout).stdout.strip()


def release_info():
    result = {}
    for line in OS_RELEASE.read_text().splitlines():
        if '=' in line and not line.startswith('#'):
            key, value = line.split('=', 1)
            values = shlex.split(value)
            if len(values) == 1:
                result[key] = values[0]
    return result


def version_key(release):
    match = re.fullmatch(r'(\d+)\.(\d+)\.(\d+)-(\d+)-generic', release)
    return tuple(map(int, match.groups())) if match else ()


def installed_kernels():
    return sorted((p.name[len('vmlinuz-'):] for p in BOOT.glob('vmlinuz-*-generic')
                   if p.is_file() and version_key(p.name[len('vmlinuz-'):])), key=version_key)


def verify_kernel(release):
    """Check disk artifacts for a future boot, without modprobe on the live kernel."""
    if not version_key(release) or affected_kernel('ubuntu', release):
        raise RuntimeError(tr('目标内核无效或仍有已知 L2TP 问题: ') + release)
    if run(['dpkg-query', '-W', '-f=${Status}', 'linux-image-' + release]) != 'install ok installed':
        raise RuntimeError(tr('目标内核软件包未配置完成: ') + release)
    for name in ('vmlinuz-' + release, 'initrd.img-' + release, 'config-' + release):
        path = BOOT / name
        if not path.is_file() or path.stat().st_size == 0:
            raise RuntimeError(tr('缺少目标内核启动文件: ') + str(path))
    for module in ('tun', 'ppp_generic', 'l2tp_ppp'):
        if not run(['modinfo', '-k', release, '-F', 'filename', module]):
            raise RuntimeError(tr('目标内核缺少模块: ') + module)
    if not re.search(r'/vmlinuz-' + re.escape(release) + r'(?:\s|$)', (BOOT / 'grub/grub.cfg').read_text()):
        raise RuntimeError(tr('GRUB 中未找到目标内核启动项: ') + release)


def check_bootloader():
    if not (BOOT / 'grub/grub.cfg').is_file() or not GRUB_DEFAULTS.is_file():
        raise RuntimeError(tr('自动准备内核仅支持标准 GRUB 主机；请按手动内核升级文档操作'))
    settings = {}
    defaults = [GRUB_DEFAULTS] + sorted(GRUB_DEFAULTS.with_name('grub.d').glob('*.cfg'))
    for path in defaults:
        for line in path.read_text().splitlines():
            if re.match(r'^\s*GRUB_(DEFAULT|TIMEOUT|TIMEOUT_STYLE)\s*=', line):
                key, value = line.strip().split('=', 1)
                values = shlex.split(value, comments=True)
                settings[key.strip()] = values[0] if len(values) == 1 else ''
    # Do not rewrite a host's saved/custom boot choice or claim a reboot will
    # activate our kernel when that choice would keep the old one selected.
    if settings.get('GRUB_DEFAULT', '0') != '0':
        raise RuntimeError(tr('GRUB 使用自定义默认启动项；请手动选择内核，安装器不会覆盖启动选择'))
    if settings.get('GRUB_TIMEOUT') == '0' or settings.get('GRUB_TIMEOUT_STYLE') == 'hidden':
        print(tr('[WARN] GRUB 菜单可能隐藏；重启前确认主机商控制台和旧内核回退入口可用'), flush=True)


def check_space():
    # Standard Noble cloud images have a roughly 1 GiB /boot partition.
    # Reserve room for a kernel and initramfs without requiring it to be empty.
    for path, minimum in ((BOOT, 512 * 1024 ** 2), (Path('/'), 2 * 1024 ** 3)):
        if shutil.disk_usage(path).free < minimum:
            raise RuntimeError(tr('{path} 可用空间不足，至少需要 {minimum} MiB；请手动处理，安装器不会删除旧内核').format(path=path, minimum=minimum // 1024 ** 2))


def check_packages():
    if run(['dpkg', '--audit']):
        raise RuntimeError(tr('存在未完成的软件包配置；请先检查 dpkg --audit，再重试内核升级'))


def backup_boot_settings():
    destination = Path(tempfile.mkdtemp(prefix='s2l-kernel-', dir=BACKUPS))
    shutil.copy2(GRUB_DEFAULTS, destination / 'grub')
    dropins = GRUB_DEFAULTS.with_name('grub.d')
    if dropins.is_dir():
        shutil.copytree(dropins, destination / 'grub.d', symlinks=True)
    shutil.copy2(BOOT / 'grub/grub.cfg', destination / 'grub.cfg')
    (destination / 'running-kernel.txt').write_text(os.uname().release + '\n')
    print(tr('[INFO] 启动配置备份: ') + str(destination), flush=True)


def apt(*args):
    # Coreutils timeout supervises apt and dpkg as one process group. Preserve
    # authentication, retain existing packages, bound locks/network/total time.
    command = ['timeout', '--kill-after=30s', '1800s', 'apt-get',
               '-o', 'DPkg::Lock::Timeout=120', '-o', 'Acquire::Retries=2',
               '-o', 'Acquire::http::Timeout=20', '-o', 'Acquire::https::Timeout=20', *args]
    env = dict(os.environ, DEBIAN_FRONTEND='noninteractive', NEEDRESTART_MODE='l', LC_ALL='C')
    subprocess.run(command, check=True, env=env, timeout=1840)


def reboot_notice(release, upgrade=False):
    print(tr('[INFO] 内核启动文件与 PPP/L2TP/TUN 模块已检查: ') + release, flush=True)
    if upgrade:
        print(tr('[OK] 新内核已准备，等待人工重启生效；现有 S2L 配置无需重装。'), flush=True)
    else:
        print(tr('[WARN] 尚未切换正在运行的内核；S2L 安装未完成，退出码 20（等待人工重启）。'), flush=True)
    print(tr('[WARN] 重启会断开 SSH/VPN。确认维护窗口与控制台回退入口后，手动执行 sudo reboot。'), flush=True)
    if upgrade:
        print(tr('[INFO] 重连后检查 uname -r，并确认 L2TP、DNS 和出口恢复。'), flush=True)
    else:
        print(tr('[INFO] 重启后检查 uname -r，然后使用原参数重新运行安装命令；安装器不会自动续装或保存密码。'), flush=True)
    print(tr('[INFO] 安装的内核软件包会保留；未进行真实拨号验证，不代表所有新内核均兼容。'), flush=True)
    return 0 if upgrade else REBOOT_REQUIRED


def prepare(upgrade=False):
    if upgrade:
        maintenance_system()
    else:
        system(check_kernel=False)
    policy = 'auto' if upgrade else os.environ.get('S2L_KERNEL_UPGRADE', 'auto')
    if policy not in ('auto', 'off'):
        raise RuntimeError(tr('S2L_KERNEL_UPGRADE 只能为 auto 或 off'))
    info = release_info()
    distro, running = info.get('ID'), os.uname().release
    installed = installed_kernels() if distro == 'ubuntu' else []
    newest = installed[-1] if installed else running
    running_bad = affected_kernel(distro, running)
    next_bad = affected_kernel(distro, newest)
    print(tr('[INFO] 当前内核: ') + running, flush=True)
    if not upgrade and not running_bad and not next_bad:
        print(tr('[INFO] 未命中已知问题内核；继续安装（不代表所有内核均已验证）。'), flush=True)
        return 0
    if running_bad or next_bad:
        print(tr('[WARN] 已知 L2TP 内核问题 2169477；当前: ') + running + tr('，已安装的最高 generic 内核: ') + newest, flush=True)
    if policy == 'off':
        raise RuntimeError(tr('已关闭自动准备内核；请手动切换兼容内核后重新运行安装。S2L 尚未部署。'))
    if distro != 'ubuntu' or info.get('VERSION_ID') != '24.04' or not version_key(running):
        raise RuntimeError(tr('该系统没有受支持的自动内核升级路径；请手动处理后重试'))
    container = subprocess.run(['systemd-detect-virt', '--quiet', '--container'],
                               capture_output=True, timeout=15).returncode
    if container == 0:
        raise RuntimeError(tr('容器不能更换宿主机内核；请在宿主机处理'))
    if container != 1:
        raise RuntimeError(tr('无法确认主机是否为容器；请手动检查后重试'))
    check_bootloader()
    check_packages()
    # A prior run may already have installed the replacement. Do not repeatedly
    # download packages while the operator has yet to reboot into that kernel.
    if not next_bad and version_key(newest) > version_key(running):
        try:
            verify_kernel(newest)
        except (RuntimeError, OSError, subprocess.SubprocessError) as error:
            print(tr('[WARN] 已安装的新内核尚未通过完整性检查: ') + str(error), flush=True)
        else:
            return reboot_notice(newest, upgrade)
    check_space()
    backup_boot_settings()
    print(tr('[INFO] 从系统 APT 源准备 Ubuntu 24.04 HWE 内核；保留旧内核，不自动重启。'), flush=True)
    apt('update')
    apt('install', '--assume-yes', '--no-remove', '--no-install-recommends', HWE_PACKAGE, 'kmod')
    kernels = installed_kernels()
    if not kernels or version_key(kernels[-1]) < version_key(running) or (not upgrade and version_key(kernels[-1]) == version_key(running)):
        raise RuntimeError(tr('APT 未提供更新的 generic 内核；请检查软件源，勿继续部署 S2L'))
    target = kernels[-1]
    verify_kernel(target)
    if target == running:
        print(tr('[OK] 当前已运行 APT 提供的目标内核，无需为此次操作重启。'), flush=True)
        return 0
    return reboot_notice(target, upgrade)


def main():
    parser = argparse.ArgumentParser(description=tr('准备 Ubuntu 24.04 HWE 内核；保留旧内核，不自动重启。'))
    parser.add_argument('--upgrade', action='store_true', help=tr('维护已有服务器；省略时执行首次安装检查'))
    args = parser.parse_args()
    try:
        require_root()
        import fcntl
        with ExitStack() as locks:
            # Do not overlap an application install/update or another kernel
            # preparation. Fresh installation already holds s2l-install.lock.
            names = ['s2l-install', 's2l-update', 's2l-kernel'] if args.upgrade else ['s2l-kernel']
            for name in names:
                descriptor = os.open('/run/' + name + '.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
                lock = locks.enter_context(os.fdopen(descriptor, 'r+'))
                try:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                except BlockingIOError as error:
                    raise RuntimeError(tr('另一个安装或升级正在运行，请完成后重试')) from error
            return prepare(upgrade=args.upgrade)
    except (RuntimeError, OSError, subprocess.SubprocessError, ValueError) as error:
        print(tr('[ERROR] 内核准备未完成: ') + str(error), file=sys.stderr)
        print(tr('[ERROR] 请勿重启；已安装的软件包保留。检查 dpkg --audit、启动文件和软件源后重试。'), file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())

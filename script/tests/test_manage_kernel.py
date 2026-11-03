from contextlib import ExitStack, redirect_stdout
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import manage_kernel as kernel


class KernelPreparationTests(unittest.TestCase):
    def setUp(self):
        self.stack = ExitStack()
        self.addCleanup(self.stack.close)
        self.output = io.StringIO()
        self.stack.enter_context(redirect_stdout(self.output))
        self.stack.enter_context(patch.dict(os.environ, {'S2L_KERNEL_UPGRADE': 'auto'}))
        self.host = self.stack.enter_context(patch.object(kernel, 'system'))
        self.maintenance = self.stack.enter_context(patch.object(kernel, 'maintenance_system'))
        self.stack.enter_context(patch.object(kernel, 'release_info', return_value={'ID': 'ubuntu', 'VERSION_ID': '24.04'}))
        self.uname = self.stack.enter_context(patch.object(kernel.os, 'uname', return_value=SimpleNamespace(release='6.8.0-147-generic'), create=True))
        self.installed = self.stack.enter_context(patch.object(kernel, 'installed_kernels', return_value=['6.8.0-147-generic']))
        self.boot = self.stack.enter_context(patch.object(kernel, 'check_bootloader'))
        self.space = self.stack.enter_context(patch.object(kernel, 'check_space'))
        self.packages = self.stack.enter_context(patch.object(kernel, 'check_packages'))
        self.backup = self.stack.enter_context(patch.object(kernel, 'backup_boot_settings'))
        self.verify = self.stack.enter_context(patch.object(kernel, 'verify_kernel'))
        self.apt = self.stack.enter_context(patch.object(kernel, 'apt'))
        self.command = self.stack.enter_context(patch.object(kernel.subprocess, 'run', return_value=SimpleNamespace(returncode=1)))

    def test_existing_host_rejected_before_package_or_boot_changes(self):
        self.host.side_effect = RuntimeError('existing tunnel service is active')
        with self.assertRaisesRegex(RuntimeError, 'existing tunnel'):
            kernel.prepare()
        self.installed.assert_not_called()
        self.apt.assert_not_called()
        self.boot.assert_not_called()
        self.command.assert_not_called()

    def test_unaffected_host_never_installs_or_reboots(self):
        self.uname.return_value.release = '7.0.0-38-generic'
        self.installed.return_value += ['7.0.0-38-generic']
        self.assertEqual(kernel.prepare(), 0)
        self.apt.assert_not_called()
        self.command.assert_not_called()

    def test_off_does_not_bypass_the_regression(self):
        with patch.dict(os.environ, {'S2L_KERNEL_UPGRADE': 'off'}):
            with self.assertRaisesRegex(RuntimeError, '关闭自动'):
                kernel.prepare()
        self.apt.assert_not_called()

    def test_invalid_policy_never_installs(self):
        with patch.dict(os.environ, {'S2L_KERNEL_UPGRADE': 'yes'}):
            with self.assertRaisesRegex(RuntimeError, 'auto 或 off'):
                kernel.prepare()
        self.apt.assert_not_called()

    def test_unknown_distribution_release_has_no_automatic_upgrade(self):
        with patch.object(kernel, 'release_info', return_value={'ID': 'ubuntu', 'VERSION_ID': '22.04'}):
            with self.assertRaisesRegex(RuntimeError, '没有受支持'):
                kernel.prepare()
        self.apt.assert_not_called()

    def test_debian_does_not_get_an_ubuntu_kernel(self):
        with patch.object(kernel, 'release_info', return_value={'ID': 'debian', 'VERSION_ID': '12'}):
            self.assertEqual(kernel.prepare(), 0)
        self.apt.assert_not_called()
        self.installed.assert_not_called()

    def test_container_is_rejected_before_apt(self):
        self.command.return_value.returncode = 0
        with self.assertRaisesRegex(RuntimeError, '容器'):
            kernel.prepare()
        self.apt.assert_not_called()

    def test_failed_container_detection_does_not_install(self):
        self.command.return_value.returncode = 2
        with self.assertRaisesRegex(RuntimeError, '无法确认'):
            kernel.prepare()
        self.apt.assert_not_called()

    def test_upgrade_requires_manual_reboot_and_complete_artifacts(self):
        self.installed.side_effect = [['6.8.0-147-generic'], ['6.8.0-147-generic', '7.0.0-38-generic']]
        self.assertEqual(kernel.prepare(), 20)
        self.verify.assert_called_once_with('7.0.0-38-generic')
        self.assertEqual(self.apt.call_args_list[0].args, ('update',))
        install = self.apt.call_args_list[1].args
        self.assertIn('--no-remove', install)
        self.assertIn('linux-generic-hwe-24.04', install)
        self.assertIn('kmod', install)
        self.assertIn('S2L 安装未完成', self.output.getvalue())
        self.command.assert_called_once_with(['systemd-detect-virt', '--quiet', '--container'],
                                             capture_output=True, timeout=15)

    def test_repeated_run_reuses_prepared_kernel(self):
        self.installed.return_value += ['7.0.0-38-generic']
        self.assertEqual(kernel.prepare(), 20)
        self.apt.assert_not_called()
        self.verify.assert_called_once_with('7.0.0-38-generic')

    def test_known_bad_pending_kernel_is_detected_on_good_running_kernel(self):
        self.uname.return_value.release = '6.8.0-31-generic'
        self.installed.side_effect = [['6.8.0-31-generic', '6.8.0-147-generic'], ['6.8.0-31-generic', '6.8.0-147-generic', '7.0.0-38-generic']]
        self.assertEqual(kernel.prepare(), 20)
        self.assertTrue(self.apt.called)

    def test_insufficient_space_does_not_start_apt(self):
        self.space.side_effect = RuntimeError('空间不足')
        with self.assertRaisesRegex(RuntimeError, '空间不足'):
            kernel.prepare()
        self.apt.assert_not_called()

    def test_failed_apt_never_reports_reboot_ready(self):
        self.apt.side_effect = subprocess.CalledProcessError(100, ['apt-get'])
        with self.assertRaises(subprocess.CalledProcessError):
            kernel.prepare()
        self.verify.assert_not_called()
        self.assertNotIn('退出码 20', self.output.getvalue())

    def test_incomplete_package_state_stops_before_backup_and_apt(self):
        self.packages.side_effect = RuntimeError('未完成的软件包配置')
        with self.assertRaisesRegex(RuntimeError, '未完成'):
            kernel.prepare(upgrade=True)
        self.backup.assert_not_called()
        self.apt.assert_not_called()

    def test_backup_failure_stops_before_apt(self):
        self.backup.side_effect = OSError('backup disk full')
        with self.assertRaisesRegex(OSError, 'disk full'):
            kernel.prepare(upgrade=True)
        self.apt.assert_not_called()

    def test_maintenance_prepares_kernel_without_fresh_install_or_service_changes(self):
        self.installed.side_effect = [['6.8.0-147-generic'], ['6.8.0-147-generic', '7.0.0-38-generic']]
        self.assertEqual(kernel.prepare(upgrade=True), 0)
        self.maintenance.assert_called_once()
        self.host.assert_not_called()
        self.backup.assert_called_once()
        self.verify.assert_called_once_with('7.0.0-38-generic')
        self.assertIn('等待人工重启生效', self.output.getvalue())
        self.assertNotIn('安装未完成', self.output.getvalue())
        # Every actual external command is accounted for: no systemctl stop,
        # reboot, module load or interface/routing mutation can be hidden here.
        self.command.assert_called_once_with(['systemd-detect-virt', '--quiet', '--container'],
                                             capture_output=True, timeout=15)

    def test_maintenance_rerun_reuses_prepared_kernel_without_package_changes(self):
        self.installed.return_value += ['7.0.0-38-generic']
        self.assertEqual(kernel.prepare(upgrade=True), 0)
        self.backup.assert_not_called()
        self.apt.assert_not_called()
        self.assertIn('等待人工重启生效', self.output.getvalue())

    def test_current_kernel_after_maintenance_needs_no_reboot(self):
        self.uname.return_value.release = '7.0.0-38-generic'
        self.installed.return_value += ['7.0.0-38-generic']
        self.assertEqual(kernel.prepare(upgrade=True), 0)
        self.verify.assert_called_once_with('7.0.0-38-generic')
        self.assertIn('无需为此次操作重启', self.output.getvalue())
        self.assertNotIn('等待人工重启', self.output.getvalue())

    def test_explicit_maintenance_does_not_use_fresh_install_off_policy(self):
        self.installed.return_value += ['7.0.0-38-generic']
        with patch.dict(os.environ, {'S2L_KERNEL_UPGRADE': 'off'}):
            self.assertEqual(kernel.prepare(upgrade=True), 0)

    def test_maintenance_rejects_debian_and_custom_kernel_before_mutation(self):
        with patch.object(kernel, 'release_info', return_value={'ID': 'debian', 'VERSION_ID': '12'}):
            with self.assertRaisesRegex(RuntimeError, '没有受支持'):
                kernel.prepare(upgrade=True)
        self.uname.return_value.release = '7.0.0-38-aws'
        with self.assertRaisesRegex(RuntimeError, '没有受支持'):
            kernel.prepare(upgrade=True)
        self.backup.assert_not_called()
        self.apt.assert_not_called()

    def test_maintenance_never_downgrades_running_kernel(self):
        self.uname.return_value.release = '7.0.0-40-generic'
        self.installed.return_value = ['7.0.0-38-generic']
        with self.assertRaisesRegex(RuntimeError, '未提供更新'):
            kernel.prepare(upgrade=True)
        self.verify.assert_not_called()

    def test_incomplete_new_kernel_never_reports_success(self):
        self.installed.side_effect = [['6.8.0-147-generic'], ['7.0.0-38-generic']]
        self.verify.side_effect = RuntimeError('缺少模块')
        with self.assertRaisesRegex(RuntimeError, '缺少模块'):
            kernel.prepare()
        self.assertNotIn('退出码 20', self.output.getvalue())

    def test_no_new_kernel_from_apt_is_rejected(self):
        with self.assertRaisesRegex(RuntimeError, '未提供更新'):
            kernel.prepare()
        self.verify.assert_not_called()


class KernelArtifactsTests(unittest.TestCase):
    def setUp(self):
        self.stack = ExitStack()
        self.addCleanup(self.stack.close)
        self.root = Path(self.stack.enter_context(tempfile.TemporaryDirectory()))
        self.boot = self.root / 'boot'
        (self.boot / 'grub').mkdir(parents=True)
        self.defaults = self.root / 'grub'
        self.defaults.write_text('GRUB_DEFAULT=0\nGRUB_TIMEOUT=5\n')
        self.stack.enter_context(patch.object(kernel, 'BOOT', self.boot))
        self.stack.enter_context(patch.object(kernel, 'GRUB_DEFAULTS', self.defaults))
        self.version = '7.0.0-38-generic'
        for name in ('vmlinuz-', 'initrd.img-', 'config-'):
            (self.boot / (name + self.version)).write_bytes(b'fixture')
        (self.boot / 'grub/grub.cfg').write_text('linux /vmlinuz-' + self.version + ' root=UUID=fixture\n')
        self.command = self.stack.enter_context(patch.object(kernel, 'run', side_effect=lambda args: 'install ok installed' if args[0]=='dpkg-query' else '/lib/modules/'+args[-1]+'.ko'))

    def test_complete_kernel_uses_offline_module_inspection(self):
        kernel.check_bootloader()
        kernel.verify_kernel(self.version)
        self.assertEqual(len(self.command.call_args_list), 4)
        for call in self.command.call_args_list[1:]:
            self.assertEqual(call.args[0][:4], ['modinfo', '-k', self.version, '-F'])

    def test_missing_initrd_module_and_menu_are_rejected(self):
        path = self.boot / ('initrd.img-' + self.version)
        path.unlink()
        with self.assertRaisesRegex(RuntimeError, '启动文件'):
            kernel.verify_kernel(self.version)
        path.write_bytes(b'initrd')
        self.command.side_effect = subprocess.CalledProcessError(1, ['modinfo'])
        with self.assertRaises(subprocess.CalledProcessError):
            kernel.verify_kernel(self.version)
        self.command.side_effect = lambda args: 'install ok installed' if args[0]=='dpkg-query' else '/lib/modules/test.ko'
        (self.boot / 'grub/grub.cfg').write_text('linux /vmlinuz-6.8.0-147-generic\n')
        with self.assertRaisesRegex(RuntimeError, 'GRUB'):
            kernel.verify_kernel(self.version)

    def test_broken_kernel_is_not_a_replacement(self):
        with self.assertRaisesRegex(RuntimeError, '已知 L2TP'):
            kernel.verify_kernel('6.8.0-147-generic')
        self.command.assert_not_called()

    def test_custom_grub_default_and_dropin_are_not_overwritten(self):
        self.defaults.write_text('GRUB_DEFAULT="saved"\n')
        with self.assertRaisesRegex(RuntimeError, '自定义'):
            kernel.check_bootloader()
        self.defaults.write_text('GRUB_DEFAULT=0\n')
        directory=self.defaults.with_name('grub.d');directory.mkdir()
        (directory/'99-custom.cfg').write_text('GRUB_DEFAULT="Advanced options>older kernel"\n')
        with self.assertRaisesRegex(RuntimeError, '自定义'):
            kernel.check_bootloader()
        self.assertEqual(self.defaults.read_text(), 'GRUB_DEFAULT=0\n')

    def test_numeric_kernel_order_ignores_other_flavors(self):
        for version in ('6.8.0-147-generic', '6.8.0-99-generic', '9.0.0-2-generic', '10.0.0-1-generic', '7.0.0-40-azure'):
            (self.boot/('vmlinuz-'+version)).write_bytes(b'image')
        self.assertEqual(kernel.installed_kernels()[-1], '10.0.0-1-generic')
        self.assertNotIn('7.0.0-40-azure', kernel.installed_kernels())

    def test_apt_retains_authentication_and_disables_service_restarts(self):
        with patch.object(kernel.subprocess,'run') as run:
            kernel.apt('install', '--assume-yes', '--no-remove', kernel.HWE_PACKAGE)
        command=run.call_args.args[0]
        self.assertEqual(command[:3], ['timeout','--kill-after=30s','1800s'])
        self.assertNotIn('--allow-unauthenticated',command)
        self.assertEqual(run.call_args.kwargs['env']['NEEDRESTART_MODE'],'l')
        self.assertEqual(run.call_args.kwargs['env']['DEBIAN_FRONTEND'],'noninteractive')

    def test_backup_preserves_boot_choices_and_keeps_copies_private(self):
        dropins = self.defaults.with_name('grub.d')
        dropins.mkdir()
        (dropins / '50-fixture.cfg').write_text('GRUB_TIMEOUT=8\n')
        with patch.object(kernel, 'BACKUPS', self.root), \
                patch.object(kernel.os, 'uname', return_value=SimpleNamespace(release='6.8.0-147-generic'), create=True):
            kernel.backup_boot_settings()
        destination, = self.root.glob('s2l-kernel-*')
        self.assertEqual((destination / 'grub').read_bytes(), self.defaults.read_bytes())
        self.assertEqual((destination / 'grub.d/50-fixture.cfg').read_text(), 'GRUB_TIMEOUT=8\n')
        self.assertEqual((destination / 'grub.cfg').read_bytes(), (self.boot / 'grub/grub.cfg').read_bytes())
        self.assertEqual((destination / 'running-kernel.txt').read_text().strip(), '6.8.0-147-generic')
        if sys.platform == 'linux':
            self.assertEqual(destination.stat().st_mode & 0o777, 0o700)

    def test_package_audit_refuses_pending_configuration(self):
        self.command.side_effect = None
        self.command.return_value = 'linux-image fixture is unpacked but not configured'
        with self.assertRaisesRegex(RuntimeError, '未完成'):
            kernel.check_packages()
        self.command.return_value = ''
        kernel.check_packages()


@unittest.skipUnless(sys.platform == 'linux', 'requires Linux shell and advisory locks')
class KernelCommandTests(unittest.TestCase):
    def test_cli_alias_and_menu_number_forward_only_to_kernel_helper(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            log = root / 'calls'
            commands = {
                'id': '#!/bin/sh\necho 0\n',
                'python3': '#!/bin/sh\nprintf "%s\\n" "$*" >> "$KERNEL_CLI_LOG"\nexit "${KERNEL_CLI_EXIT:-0}"\n',
                'systemctl': '#!/bin/sh\necho unexpected-service-change >> "$KERNEL_CLI_LOG"\nexit 99\n',
            }
            for name, content in commands.items():
                path = root / name
                path.write_text(content)
                path.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'], KERNEL_CLI_LOG=str(log))
            cli = Path(__file__).resolve().parents[1] / 's2l.sh'
            for argument in ('kernel-upgrade', '12'):
                result = subprocess.run(['bash', str(cli), argument], env=env, capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(log.read_text().splitlines(), ['/opt/s2l/manage_kernel.py --upgrade'] * 2)
            env['KERNEL_CLI_EXIT'] = '7'
            result = subprocess.run(['bash', str(cli), 'kernel-upgrade'], env=env, capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 7)

    def test_maintenance_conflicting_operation_never_reaches_preparation(self):
        import fcntl
        real_open = os.open
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with (root / 's2l-update.lock').open('w') as busy:
                fcntl.flock(busy, fcntl.LOCK_EX | fcntl.LOCK_NB)
                def open_lock(path, flags, mode=0o777):
                    self.assertIn(path, ['/run/s2l-install.lock', '/run/s2l-update.lock', '/run/s2l-kernel.lock'])
                    return real_open(root / Path(path).name, flags, mode)
                with patch.object(sys, 'argv', ['manage_kernel.py', '--upgrade']), \
                        patch.object(kernel, 'require_root'), patch.object(kernel, 'prepare') as prepare, \
                        patch.object(kernel.os, 'open', side_effect=open_lock):
                    self.assertEqual(kernel.main(), 1)
                    prepare.assert_not_called()
            # The earlier install lock must have been released on failure.
            with (root / 's2l-install.lock').open('r+') as freed:
                fcntl.flock(freed, fcntl.LOCK_EX | fcntl.LOCK_NB)

    def test_maintenance_can_run_from_installed_helper_without_installer_modules(self):
        # Copy only the runtime files deployed by install/update. A real interpreter must
        # reach maintenance mode without importing the fresh-host preflight.
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'manage_kernel.py'
            path.write_bytes(Path(kernel.__file__).read_bytes())
            path.with_name('installer_i18n.py').write_bytes(Path(kernel.__file__).with_name('installer_i18n.py').read_bytes())
            command = ('import manage_kernel as k; k.maintenance_system=lambda: None; '
                       "k.release_info=lambda: {'ID':'debian','VERSION_ID':'12'}; "
                       'k.prepare(upgrade=True)')
            result = subprocess.run([sys.executable, '-c', command], cwd=directory,
                                    capture_output=True, text=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('没有受支持', result.stderr)
            self.assertNotIn('ImportError', result.stderr)


if __name__ == '__main__':
    unittest.main()

import importlib.util
from pathlib import Path
import unittest
import os
import tempfile
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('manage_time', Path(__file__).resolve().parents[1] / 'manage_time.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class TimeSettingsTests(unittest.TestCase):
    def body(self, **changes):
        return {'timezone': 'Asia/Shanghai', 'auto_sync': True, 'mode': 'custom', 'servers': ['ntp.example.com', '192.0.2.1'], **changes}

    def test_validate_rejects_injection_and_non_boolean_switch(self):
        module.validate(self.body(), ['Asia/Shanghai'])
        for host in ['a\nNTP=evil', '-x', 'https://a', 'a:123', 'x;id', 'a..com', '-a.com', 'a-.com', 'fe80::1%eth0\nNTP=evil', 'fe80::1%eth0', 'fe80::1%x\rFallbackNTP=evil']:
            with self.subTest(host=host), self.assertRaises(ValueError):
                module.validate(self.body(servers=[host]), ['Asia/Shanghai'])
        for body in [self.body(auto_sync='true'), self.body(timezone='../../etc/passwd'), self.body(servers=[]), self.body(servers=['a'] * 9)]:
            with self.assertRaises(ValueError):
                module.validate(body, ['Asia/Shanghai'])

    def test_incomplete_system_status_is_rejected_before_changes(self):
        with patch.object(module, 'run', return_value='Timezone=UTC\nNTP=yes'):
            with self.assertRaisesRegex(RuntimeError, '状态不完整'):
                module.status()

    def test_failure_restores_file_timezone_and_ntp(self):
        original = (module.HEADER + '[Time]\nNTP=old.example\n').encode()
        disk = {'data': original}
        commands = []
        previous = {'timezone': 'UTC', 'auto_sync': False, 'custom_available': True}
        def run(*args):
            commands.append(args)
            if args == ('timedatectl', 'set-ntp', 'true'):
                raise RuntimeError('simulated failure')
            return ''
        with patch.object(module, 'zones', return_value=['Asia/Shanghai']), patch.object(module, 'status', return_value=previous), patch.object(module, 'read_config', return_value=original), patch.object(module, 'publish', side_effect=lambda data: disk.update(data=data)), patch.object(module, 'loaded', return_value=True), patch.object(module, 'run', side_effect=run):
            with self.assertRaisesRegex(RuntimeError, '已恢复原配置'):
                module.apply(self.body())
        self.assertEqual(disk['data'], original)
        self.assertIn(('timedatectl', 'set-timezone', 'UTC'), commands)
        self.assertIn(('timedatectl', 'set-ntp', 'false'), commands)

    def test_custom_ntp_conflict_causes_no_mutation(self):
        with patch.object(module, 'zones', return_value=['Asia/Shanghai']), patch.object(module, 'status', return_value={'custom_available': False}), patch.object(module, 'read_config', return_value=None), patch.object(module, 'publish') as publish, patch.object(module, 'run') as run:
            with self.assertRaises(ValueError):
                module.apply(self.body())
            publish.assert_not_called()
            run.assert_not_called()

    def test_success_uses_arguments_and_does_not_claim_synchronized(self):
        previous = {'timezone': 'UTC', 'auto_sync': False, 'custom_available': True}
        with patch.object(module, 'zones', return_value=['Asia/Shanghai']), patch.object(module, 'status', return_value=previous), patch.object(module, 'read_config', return_value=None), patch.object(module, 'publish') as publish, patch.object(module, 'run') as run:
            result = module.apply(self.body())
            self.assertIn(b'NTP=ntp.example.com 192.0.2.1\nFallbackNTP=\n', publish.call_args.args[0])
            self.assertIn(('timedatectl', 'set-ntp', 'true'), [call.args for call in run.call_args_list])
            self.assertNotIn('synchronized', result)

    @unittest.skipUnless(os.name == 'posix', 'Linux atomic configuration publishing')
    def test_atomic_configuration_publish_and_removal(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'conf.d' / '90-s2l.conf'
            with patch.object(module, 'CONFIG', path):
                module.publish(b'first')
                module.publish(b'second')
                self.assertEqual(path.read_bytes(), b'second')
                self.assertEqual(path.stat().st_mode & 0o777, 0o644)
                module.publish(None)
                self.assertFalse(path.exists())
                self.assertEqual(list(path.parent.iterdir()), [])

    def test_backend_detection_fails_closed(self):
        with patch.object(module, 'loaded', side_effect=RuntimeError('unavailable')):
            self.assertFalse(module.custom_available())

    def test_missing_unit_is_not_a_service_conflict(self):
        from subprocess import CompletedProcess
        with patch.object(module.subprocess, 'run', return_value=CompletedProcess([], 1, 'not-found\n', '')):
            self.assertFalse(module.loaded('missing.service'))

    def test_rollback_failure_is_reported(self):
        previous = {'timezone': 'UTC', 'auto_sync': True, 'custom_available': True}
        with patch.object(module, 'zones', return_value=['Asia/Shanghai']), patch.object(module, 'status', return_value=previous), patch.object(module, 'read_config', return_value=None), patch.object(module, 'publish', side_effect=OSError('disk full')), patch.object(module, 'loaded', return_value=True), patch.object(module, 'run', return_value=''):
            with self.assertRaisesRegex(RuntimeError, '回滚未完全成功'):
                module.apply(self.body())


if __name__ == '__main__':
    unittest.main()

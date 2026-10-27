from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.append(str(Path(__file__).resolve().parents[1]))
import dependencies


class DependencyTests(unittest.TestCase):
    def test_shared_reverse_dependency_prevents_package_removal(self):
        value = {'packages': {'ppp': {}}, 'config_paths': []}
        def command(*args, **kwargs):
            if args[0] == 'apt-get':
                return subprocess.CompletedProcess(args, 0, 'Purg ppp [1]\nRemv foreign-vpn [2]\n')
            return subprocess.CompletedProcess(args, 3, '')
        with patch.object(dependencies, 'installed', return_value=True), patch.object(dependencies, 'config_paths', return_value=[]), patch.object(dependencies, 'run', side_effect=command):
            self.assertEqual(dependencies.package_plan(value), ([], ['ppp']))

    def test_foreign_configuration_and_new_peer_preserve_packages(self):
        value = {'packages': {'ppp': {'/etc/ppp/chap-secrets': 'original'}}, 'config_paths': []}
        with patch.object(dependencies, 'installed', return_value=True), patch.object(dependencies, 'config_paths', return_value=[]), patch.object(dependencies, 'fingerprint', return_value='changed'):
            self.assertEqual(dependencies.package_plan(value), ([], ['ppp']))
        with patch.object(dependencies, 'installed', return_value=True), patch.object(dependencies, 'config_paths', return_value=['/etc/ppp/peers/foreign']), patch.object(dependencies, 'fingerprint', return_value='original'):
            self.assertEqual(dependencies.package_plan(value), ([], ['ppp']))

    @unittest.skipUnless(sys.platform == 'linux', 'Linux ownership receipt')
    def test_existing_singbox_is_never_claimed_on_begin_or_record(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            binary = root / 'sing-box'
            binary.write_bytes(b'existing binary')
            with patch.object(dependencies, 'ROOT', root), patch.object(dependencies, 'RECEIPT', root / 'receipt.json'), patch.object(dependencies, 'SINGBOX', binary), patch.object(dependencies, 'installed', return_value=True), patch.object(dependencies, 'config_paths', return_value=[]):
                dependencies.begin()
                dependencies.record()
                self.assertNotIn('singbox_sha256', dependencies.read())
                self.assertEqual(binary.read_bytes(), b'existing binary')

    def test_changed_and_shared_singbox_are_retained(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / 'sing-box'
            binary.write_bytes(b'original')
            digest = dependencies.fingerprint(binary)
            value = {'singbox_sha256': digest}
            with patch.object(dependencies, 'SINGBOX', binary), patch.object(dependencies, 'read', return_value=value), patch.object(dependencies, 'package_plan', return_value=([], [])), patch.object(dependencies, 'singbox_shared', return_value=True):
                dependencies.cleanup()
                self.assertTrue(binary.exists())
            binary.write_bytes(b'changed')
            with patch.object(dependencies, 'SINGBOX', binary), patch.object(dependencies, 'read', return_value=value), patch.object(dependencies, 'package_plan', return_value=([], [])), patch.object(dependencies, 'singbox_shared', return_value=False):
                dependencies.cleanup()
                self.assertTrue(binary.exists())

    def test_only_recorded_unshared_unchanged_singbox_is_removed(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / 'sing-box'
            binary.write_bytes(b'owned')
            value = {'singbox_sha256': dependencies.fingerprint(binary)}
            with patch.object(dependencies, 'SINGBOX', binary), patch.object(dependencies, 'read', return_value=value), patch.object(dependencies, 'package_plan', return_value=([], [])), patch.object(dependencies, 'singbox_shared', return_value=False):
                dependencies.cleanup()
                self.assertFalse(binary.exists())

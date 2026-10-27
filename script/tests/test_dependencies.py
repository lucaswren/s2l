from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.append(str(Path(__file__).resolve().parents[1]))
import dependencies
read_ppp_templates = dependencies.ppp_generated_files


class DependencyTests(unittest.TestCase):
    def setUp(self):
        sharing = patch.object(dependencies, 'sharing_groups', return_value=set())
        sharing.start()
        self.addCleanup(sharing.stop)
        templates = patch.object(dependencies, 'ppp_generated_files', return_value={})
        templates.start()
        self.addCleanup(templates.stop)
        generated_paths = patch.object(dependencies, 'PPP_GENERATED', {})
        generated_paths.start()
        self.addCleanup(generated_paths.stop)

    def test_shared_reverse_dependency_prevents_package_removal(self):
        value = {'packages': {'ppp': {}}, 'config_paths': []}
        def command(*args, **kwargs):
            if args[0] == 'apt-get':
                return subprocess.CompletedProcess(args, 0, 'Purg ppp [1]\nRemv foreign-vpn [2]\n')
            if args[0] == 'pgrep':
                return subprocess.CompletedProcess(args, 1, '')
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
            value = {'singbox_sha256': digest, 'packages': {name: {} for name in dependencies.PACKAGES}}
            with patch.object(dependencies, 'run', return_value=subprocess.CompletedProcess([], 1, '')), patch.object(dependencies, 'SINGBOX', binary), patch.object(dependencies, 'read', return_value=value), patch.object(dependencies, 'package_plan', return_value=([], [])), patch.object(dependencies, 'singbox_shared', return_value=True):
                dependencies.cleanup()
                self.assertTrue(binary.exists())
            binary.write_bytes(b'changed')
            with patch.object(dependencies, 'run', return_value=subprocess.CompletedProcess([], 1, '')), patch.object(dependencies, 'SINGBOX', binary), patch.object(dependencies, 'read', return_value=value), patch.object(dependencies, 'package_plan', return_value=([], [])), patch.object(dependencies, 'singbox_shared', return_value=False):
                dependencies.cleanup()
                self.assertTrue(binary.exists())

    def test_only_recorded_unshared_unchanged_singbox_is_removed(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / 'sing-box'
            binary.write_bytes(b'owned')
            value = {'singbox_sha256': dependencies.fingerprint(binary), 'packages': {name: {} for name in dependencies.PACKAGES}}
            with patch.object(dependencies, 'run', return_value=subprocess.CompletedProcess([], 1, '')), patch.object(dependencies, 'SINGBOX', binary), patch.object(dependencies, 'read', return_value=value), patch.object(dependencies, 'package_plan', return_value=([], [])), patch.object(dependencies, 'singbox_shared', return_value=False):
                dependencies.cleanup()
                self.assertFalse(binary.exists())

    def test_legacy_compares_original_package_digest_not_current_custom_content(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'vpn.conf'
            path.write_bytes(b'stock\n')
            digest = 'dpkg-md5:' + dependencies.hashlib.md5(b'stock\n', usedforsecurity=False).hexdigest()
            self.assertTrue(dependencies.matches_config(str(path), digest))
            path.write_bytes(b'foreign server configuration\n')
            self.assertFalse(dependencies.matches_config(str(path), digest))

    def test_shared_ppp_kept_without_retaining_dedicated_l2tp_and_ipsec(self):
        value = {'packages': {'ppp': {}, 'xl2tpd': {}, 'strongswan': {}}, 'config_paths': []}
        with patch.object(dependencies, 'installed', return_value=True), patch.object(dependencies, 'config_paths', return_value=['/etc/ppp/peers/other-vpn']), patch.object(dependencies, 'safe_removals', side_effect=lambda names, report: (names, [])):
            self.assertEqual(dependencies.package_plan(value), (['xl2tpd', 'strongswan'], ['ppp']))

    def test_shared_library_retained_without_retaining_its_dedicated_consumers(self):
        def simulate(*args, **kwargs):
            selected = args[4:]
            names = set(selected)
            if 'libstrongswan' in names:
                names.add('foreign-vpn')
            return subprocess.CompletedProcess(args, 0, ''.join('Purg ' + name + ' [1]\n' for name in names))
        with patch.object(dependencies, 'run', side_effect=simulate):
            self.assertEqual(dependencies.safe_removals(['strongswan', 'libstrongswan', 'xl2tpd'], lambda text: None), (['strongswan', 'xl2tpd'], ['libstrongswan']))

    def test_custom_unit_with_standard_name_is_shared(self):
        with tempfile.TemporaryDirectory() as directory:
            unit = Path(directory) / 'strongswan-starter.service'
            unit.write_bytes(b'[Service]\nExecStart=/usr/lib/ipsec/charon --conf /opt/other.conf\n')
            with patch.object(dependencies, 'SERVICE_ROOTS', (directory,)):
                self.assertTrue(dependencies.references((b'charon',), ('strongswan-starter.service',)))

    def test_generated_ppp_defaults_require_original_package_template(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            template = root / 'provider.peer'
            template.write_bytes(b'user "example"\n')
            checksum = dependencies.hashlib.md5(template.read_bytes(), usedforsecurity=False).hexdigest()
            manifest = subprocess.CompletedProcess([], 0, checksum + '  usr/share/ppp/provider.peer\n')
            with patch.object(dependencies, 'PPP_GENERATED', {'/etc/ppp/peers/provider': 'provider.peer'}), patch.object(dependencies, 'PPP_TEMPLATES', root), patch.object(dependencies, 'run', return_value=manifest):
                files = read_ppp_templates()
                self.assertIn('/etc/ppp/peers/provider', files)
                target = root / 'provider'
                target.write_bytes(template.read_bytes())
                self.assertTrue(dependencies.matches_config(str(target), files['/etc/ppp/peers/provider']))
                target.write_bytes(b'user "foreign-vpn"\n')
                self.assertFalse(dependencies.matches_config(str(target), files['/etc/ppp/peers/provider']))
                template.write_bytes(target.read_bytes())
                self.assertEqual(read_ppp_templates(), {})

    def test_generated_credentials_ignore_hostname_comment_but_detect_accounts(self):
        path = '/etc/ppp/pap-secrets'
        original = b'#-HOSTNAME-\n* hostname "" *\n'
        self.assertEqual(dependencies.template_digest(path, original), dependencies.template_digest(path, b'#server\n* hostname "" *\n'))
        self.assertNotEqual(dependencies.template_digest(path, original), dependencies.template_digest(path, original + b'other * password *\n'))

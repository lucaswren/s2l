from pathlib import Path
import os
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import uninstall


class UninstallTests(unittest.TestCase):
    def test_chap_removes_only_owned_pairs_and_preserves_foreign_credentials(self):
        foreign = '# retained\n"other" * "secret" 10.9.0.2\n'
        self.assertEqual(uninstall.strip_chap(foreign + '# s2l:map_1\n"owned" * "own pass" 10.0.101.2\n'), foreign)
        for invalid in ('# s2l:map_1\n', '# s2l:map_1\n# foreign comment\n', '# s2l:map_1\n"user" * "pass" *\n'):
            with self.assertRaises((ValueError, RuntimeError)):
                uninstall.strip_chap(invalid)

    def test_l2tp_refuses_foreign_sections_and_invalid_markers(self):
        owned = '; BEGIN s2l managed block - DO NOT EDIT\n[global]\nport = 1701\n; END s2l managed block\n'
        self.assertEqual(uninstall.strip_l2tp('; keep\n' + owned), '; keep\n')
        for text in (owned + '[lns foreign]\n', owned + owned, '[global]\n', owned.replace('; END s2l managed block', '')):
            with self.assertRaises(RuntimeError):
                uninstall.strip_l2tp(text)

    def test_firewall_transactions_preserve_foreign_rules_and_policies(self):
        snapshot = '''*filter
:INPUT ACCEPT [0:0]
:FORWARD DROP [0:0]
:S2L_FORWARD - [0:0]
:S2L_RETURN - [0:0]
:FOREIGN - [0:0]
-A INPUT -s 192.0.2.1/32 -j FOREIGN
-A INPUT -i ppp+ -j DROP
-A FORWARD -i ppp+ -j S2L_FORWARD
-A FORWARD -i tun101 -j S2L_RETURN
-A S2L_FORWARD -j DROP
-A S2L_RETURN -j DROP
COMMIT
*nat
:POSTROUTING ACCEPT [0:0]
-A POSTROUTING -o tun101 -m comment --comment "s2l:map_1" -j MASQUERADE
-A POSTROUTING -o eth0 -m comment --comment "foreign s2l:map_1" -j MASQUERADE
COMMIT
'''
        plan = uninstall.firewall_plan(snapshot)
        self.assertIn('-D FORWARD -i tun101 -j S2L_RETURN', plan)
        self.assertIn('-X S2L_RETURN', plan)
        self.assertIn('-D POSTROUTING -o tun101', plan)
        for forbidden in ('FOREIGN', 'eth0', '-P ', '-F INPUT', '-F FORWARD', '-F POSTROUTING'):
            self.assertNotIn(forbidden, plan)
        self.assertLess(plan.index('-F S2L_RETURN'), plan.index('-X S2L_FORWARD'))
        with self.assertRaisesRegex(RuntimeError, 'foreign firewall'):
            uninstall.firewall_plan(snapshot.replace('-A INPUT -s 192.0.2.1/32 -j FOREIGN', '-A INPUT -s 192.0.2.1/32 -j S2L_RETURN'))
        with self.assertRaises(RuntimeError):
            uninstall.firewall_plan('*filter\n:INPUT ACCEPT [0:0]\n')

    def test_ipv6_removes_only_exact_ppp_drop(self):
        snapshot = '*filter\n:INPUT ACCEPT [0:0]\n-A INPUT -i ppp+ -j DROP\n-A INPUT -i ppp+ -m comment --comment foreign -j DROP\nCOMMIT\n'
        self.assertEqual(uninstall.firewall_plan(snapshot, True), '*filter\n-D INPUT -i ppp+ -j DROP\nCOMMIT\n')

    def test_ssh_vpn_route_refuses_before_uninstall(self):
        for device in ('ppp0', 'tun101'):
            with patch.dict(os.environ, SSH_CONNECTION='192.0.2.8 40000 198.51.100.8 22'), patch.object(uninstall, 'run', return_value=subprocess.CompletedProcess([], 0, '[{"dev":"' + device + '"}]')):
                with self.assertRaises(RuntimeError):
                    uninstall.check_connection([{'tun_name': 'tun101'}])
        with patch.dict(os.environ, SSH_CONNECTION='192.0.2.8 40000 198.51.100.8 22'), patch.object(uninstall, 'run', return_value=subprocess.CompletedProcess([], 0, '[{"dev":"eth0"}]')):
            uninstall.check_connection([{'tun_name': 'tun101'}])

    def test_orphan_tun_or_ppp_keeps_guards(self):
        for device in ('ppp0', 'tun201'):
            with self.assertRaisesRegex(RuntimeError, 'guards retained'):
                uninstall.check_stopped_links([], [{'ifname': device}], '-A FORWARD -i tun201 -j S2L_RETURN')
        uninstall.check_stopped_links([], [{'ifname': 'eth0'}], '-A FORWARD -i tun201 -j S2L_RETURN')

    @unittest.skipUnless(sys.platform == 'linux', 'Linux symlinks and file modes')
    def test_symlink_targets_are_refused_and_atomic_write_keeps_mode(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / 'secret'
            path.write_bytes(b'old')
            path.chmod(0o600)
            link = Path(root) / 'link'
            link.symlink_to(path)
            with self.assertRaises(RuntimeError):
                uninstall.read_file(link)
            uninstall.write_file(path, b'new')
            self.assertEqual(path.read_bytes(), b'new')
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    @unittest.skipUnless(sys.platform == 'linux', 'Linux shortcut entrypoint')
    def test_shortcut_help_and_invalid_arguments_are_read_only(self):
        script = str(Path(__file__).resolve().parents[1] / 's2l.sh')
        result = subprocess.run(['bash', script, '--help'], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0)
        self.assertIn('uninstall --dry-run', result.stdout)
        for arguments in (['uninstall', '--yes'], ['2', '--dry-run'], ['13', '--dry-run', 'extra']):
            result = subprocess.run(['bash', script, *arguments], capture_output=True, text=True)
            self.assertEqual(result.returncode, 2)

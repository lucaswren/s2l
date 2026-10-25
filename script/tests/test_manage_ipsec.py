from pathlib import Path
import shlex
import subprocess
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
if sys.platform == 'linux':
    import manage_ipsec


@unittest.skipUnless(sys.platform == 'linux', 'IPsec manager requires Linux')
class IPsecFirewallTests(unittest.TestCase):
    def setUp(self):
        self.chain = '-N S2L-IPSEC\n' + '\n'.join(
            shlex.join(['-A', 'S2L-IPSEC', *r]) for r in manage_ipsec.firewall_rules()) + '\n'
        self.incoming = '-P INPUT ACCEPT\n-A INPUT -j S2L-IPSEC\n-A INPUT -p tcp -j ACCEPT\n'
        self.outgoing = '-P OUTPUT ACCEPT\n-A OUTPUT -j S2L-IPSEC-OUT\n'
        self.out_chain = '\n'.join(shlex.join(['-A', manage_ipsec.OUT_CHAIN, *r]) for r in manage_ipsec.outgoing_rules())

    def commands(self, *args, **kwargs):
        output = {'S2L-IPSEC': self.chain, 'INPUT': self.incoming,
                  'OUTPUT': self.outgoing, 'S2L-IPSEC-OUT': self.out_chain}.get(args[-1], '')
        return subprocess.CompletedProcess(args, 0, output, '')

    def protected(self):
        with patch.object(manage_ipsec.shutil, 'which', return_value='/usr/sbin/iptables'), \
                patch.object(manage_ipsec, 'run', side_effect=self.commands):
            return manage_ipsec.firewall_protected()

    def test_missing_drop_is_not_protected(self):
        self.assertTrue(self.protected())
        self.chain = '\n'.join(x for x in self.chain.splitlines() if '-j DROP' not in x)
        self.assertFalse(self.protected())

    def test_extra_accept_and_reordered_jump_are_not_protected(self):
        self.chain = self.chain.replace('-N S2L-IPSEC\n', '-N S2L-IPSEC\n-A S2L-IPSEC -j ACCEPT\n')
        self.assertFalse(self.protected())
        self.setUp()
        self.incoming = self.incoming.replace('-P INPUT ACCEPT\n', '-P INPUT ACCEPT\n-A INPUT -p udp -j ACCEPT\n')
        self.assertFalse(self.protected())

    def test_missing_or_bypassed_output_guard_is_not_protected(self):
        self.out_chain = ''
        self.assertFalse(self.protected())
        self.setUp()
        self.outgoing = '-A OUTPUT -j ACCEPT\n' + self.outgoing
        self.assertFalse(self.protected())

    def test_config_enforces_nat_t_and_unique_marks_with_owned_hook(self):
        conf = manage_ipsec.render_conf().decode()
        for expected in ('forceencaps=yes', 'mark=%unique', 'ipsec_nat.py', 'rightprotoport=17/%any'):
            self.assertIn(expected, conf)

    def test_encrypted_arrivals_require_a_prepared_per_sa_path(self):
        rule = next(r for r in manage_ipsec.firewall_rules() if '--pol' in r)
        self.assertEqual(rule[-2:], ['-j', manage_ipsec.PEER_CHAIN])

    def test_drift_repair_is_atomic_and_preserves_other_chains(self):
        self.incoming += '-A INPUT -j S2L-IPSEC\n'
        with patch.object(manage_ipsec, 'firewall_protected', return_value=False), \
                patch.object(manage_ipsec, 'run', side_effect=self.commands) as run:
            manage_ipsec.firewall(True)
        restore = [c for c in run.call_args_list if c.args[0] == 'iptables-restore']
        self.assertEqual(len(restore), 1)
        self.assertIn('--noflush', restore[0].args)
        rules = restore[0].kwargs['input'].splitlines()
        self.assertEqual(rules.count('-D INPUT -j S2L-IPSEC'), 2)
        self.assertIn('-I INPUT 1 -j S2L-IPSEC', rules)
        self.assertIn('-I OUTPUT 1 -j S2L-IPSEC-OUT', rules)
        self.assertNotIn('-F INPUT', rules)
        self.assertNotIn('-F S2L-IPSEC-PEERS', rules)
        self.assertNotIn(':S2L-IPSEC-PEERS - [0:0]', rules)
        self.assertEqual(rules[-1], 'COMMIT')
        self.assertFalse(any('-F' in c.args for c in run.call_args_list))

    def test_restore_failure_propagates_without_unlinking_guard(self):
        def fail(*args, **kwargs):
            if args[0] == 'iptables-restore':
                raise RuntimeError('restore failed')
            return self.commands(*args, **kwargs)
        with patch.object(manage_ipsec, 'firewall_protected', return_value=False), \
                patch.object(manage_ipsec, 'run', side_effect=fail) as run:
            with self.assertRaises(RuntimeError):
                manage_ipsec.firewall(True)
        self.assertFalse(any('-D' in c.args or '-F' in c.args for c in run.call_args_list))

    def test_healthy_guard_is_not_rebuilt(self):
        with patch.object(manage_ipsec, 'firewall_protected', return_value=True), \
                patch.object(manage_ipsec, 'run', side_effect=self.commands) as run:
            manage_ipsec.firewall(True)
        self.assertFalse(any(c.args[0] == 'iptables-restore' for c in run.call_args_list))

    def test_ppp_management_guard_can_precede_ipsec_only_if_it_drops_udp(self):
        self.incoming = '-A INPUT -i ppp+ -j S2L_MANAGEMENT\n' + self.incoming
        management = '-A S2L_MANAGEMENT -p tcp -m tcp --dport 443 -j ACCEPT\n-A S2L_MANAGEMENT -j DROP\n'
        def commands(*args, **kwargs):
            if args[-1] == 'S2L_MANAGEMENT':
                return subprocess.CompletedProcess(args, 0, management, '')
            return self.commands(*args, **kwargs)
        with patch.object(manage_ipsec.shutil, 'which', return_value='/usr/sbin/iptables'), \
                patch.object(manage_ipsec, 'run', side_effect=commands):
            self.assertTrue(manage_ipsec.firewall_protected())
            management = management.replace('-p tcp -m tcp --dport 443', '-p udp -m udp --dport 1701')
            self.assertFalse(manage_ipsec.firewall_protected())


if __name__ == '__main__':
    unittest.main()

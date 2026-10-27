from pathlib import Path
import subprocess
import json
import os
import tempfile
import threading
import time
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
if sys.platform == 'linux':
    import ipsec_nat


@unittest.skipUnless(sys.platform == 'linux', 'Linux IPsec hook')
class IPsecNATTests(unittest.TestCase):
    def setUp(self):
        self.original_capture = ipsec_nat.capture_unmarked
        capture = patch.object(ipsec_nat, 'capture_unmarked', return_value=[])
        capture.start()
        self.addCleanup(capture.stop)
        self.env = dict(PLUTO_VERB='up-host', PLUTO_ME='198.18.99.1', PLUTO_PEER='192.0.2.2',
                        PLUTO_REQID='1', PLUTO_MARK_IN='1/0xffffffff',
                        PLUTO_MARK_OUT='1/0xffffffff', PLUTO_UDP_ENC='4500')
        self.state = dict(next=ipsec_nat.FIRST, active={}, retired={}, free=[])

    def test_same_peer_uses_distinct_zone_and_internal_port_without_client_port_change(self):
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'edit_rules') as edit:
            ipsec_nat.update(self.state, self.env)
            other = dict(self.env, PLUTO_REQID='2', PLUTO_MARK_IN='2/0xffffffff', PLUTO_MARK_OUT='2/0xffffffff', PLUTO_UDP_ENC='4501')
            ipsec_nat.update(self.state, other)
        rows = list(self.state['active'].values())
        self.assertEqual([a['slot'] for a in rows], [20000, 20001])
        for row in rows:
            rules = ipsec_nat.rules(row)
            self.assertEqual(rules[-1][0:2], ('mangle', 'PREROUTING'))
            self.assertIn('1701', rules[2][2])
            self.assertEqual(rules[3][2][-1], '192.0.2.2:' + str(row['slot']))
            self.assertEqual(rules[4][0:2], ('filter', 'S2L-IPSEC-PEERS'))
            self.assertIn(row['incoming'], rules[4][2])

    def test_duplicate_and_stale_down_do_not_displace_other_sa(self):
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'edit_rules'):
            ipsec_nat.update(self.state, self.env)
            ipsec_nat.update(self.state, self.env)
            self.assertEqual(self.state['next'], 20001)
            ipsec_nat.update(self.state, dict(self.env, PLUTO_VERB='down-host', PLUTO_MARK_IN='2/0xffffffff'))
            self.assertEqual(len(self.state['active']), 1)
            ipsec_nat.update(self.state, dict(self.env, PLUTO_VERB='down-host'))
            self.assertFalse(self.state['active'])
            ipsec_nat.update(self.state, self.env)
            self.assertEqual(next(iter(self.state['active'].values()))['slot'], 20001)

    def test_failed_install_rolls_back_and_does_not_reuse_endpoint(self):
        with patch.object(ipsec_nat, 'save') as save, \
                patch.object(ipsec_nat, 'edit_rules', side_effect=[RuntimeError('failed'), None]) as edit:
            with self.assertRaises(RuntimeError):
                ipsec_nat.update(self.state, self.env)
        self.assertEqual([c.args[1] for c in edit.call_args_list], [True, False])
        self.assertFalse(self.state['active'])
        self.assertEqual(self.state['next'], 20001)
        self.assertEqual(save.call_count, 2)
        self.assertNotIn('unmarked_spis', self.state['retired']['20000'])
        with patch.object(ipsec_nat, 'save'):
            ipsec_nat.update(self.state, dict(self.env, PLUTO_VERB='down-host'))
        self.assertEqual(self.state['retired']['20000']['unmarked_spis'], [])

    def test_cleanup_preserves_retired_endpoint_budget(self):
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'edit_rules'), \
                patch.object(ipsec_nat, 'run', return_value=subprocess.CompletedProcess([], 0, 'in-use', '')):
            ipsec_nat.update(self.state, self.env)
            ipsec_nat.update(self.state, {}, cleanup=True)
        self.assertFalse(self.state['active'])
        self.assertEqual(self.state['next'], 20001)

    def test_retired_ports_reclaimed_only_after_both_stacks_stop(self):
        self.state['next'] = 65536
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'run',
                side_effect=[subprocess.CompletedProcess([], 0, '', ''),
                             subprocess.CompletedProcess([], 0, 'active-SA', '')]):
            ipsec_nat.update(self.state, {}, cleanup=True)
        self.assertEqual(self.state['next'], 65536)
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'run',
                return_value=subprocess.CompletedProcess([], 0, '', '')):
            ipsec_nat.update(self.state, {}, cleanup=True)
        self.assertEqual(self.state['next'], 20000)

    def test_missing_nat_t_bad_addresses_and_partial_marks_fail_before_mutation(self):
        for fields in ({'PLUTO_UDP_ENC': '0'}, {'PLUTO_ME': '::1'}, {'PLUTO_MARK_IN': '1/0xff'},
                       {'PLUTO_PEER': '192.0.2.2; echo bad'}, {'PLUTO_MARK_OUT': '0/0xffffffff'}):
            with self.subTest(fields=fields), patch.object(ipsec_nat, 'edit_rules') as edit:
                with self.assertRaises(ValueError):
                    ipsec_nat.update(self.state, dict(self.env, **fields))
                edit.assert_not_called()
        self.assertEqual(self.state, dict(next=20000, active={}, retired={}, free=[]))

    def test_exhaustion_fails_closed_without_wrapping(self):
        self.state['next'] = 65536
        with patch.object(ipsec_nat, 'edit_rules') as edit:
            with self.assertRaisesRegex(RuntimeError, 'pool busy'):
                ipsec_nat.update(self.state, self.env)
            edit.assert_not_called()

    def test_teardown_removes_outer_mark_first_and_only_deletes_owned_zone(self):
        def command(*args, **kwargs):
            return subprocess.CompletedProcess(args, 0, '', '')
        with patch.object(ipsec_nat, 'run', side_effect=command) as run:
            ipsec_nat.edit_rules(ipsec_nat.association(self.env, 20000), False)
        deletes = [c.args for c in run.call_args_list if '-D' in c.args]
        self.assertIn('PREROUTING', deletes[0])
        self.assertIn('4500', deletes[0])
        self.assertIn('s2l-ipsec-nat', deletes[0])
        self.assertEqual(deletes[-1][:5], ('conntrack', '-D', '-w', '20000', '-p'))

    def test_conntrack_permission_error_is_not_an_empty_zone(self):
        for message, fails in [('conntrack: Operation not permitted', True),
                               ('conntrack v1.4.8: 0 flow entries have been deleted.', False)]:
            def command(*args, **kwargs):
                return subprocess.CompletedProcess(args, 1 if args[0] == 'conntrack' else 0, '', message)
            with self.subTest(message=message), patch.object(ipsec_nat, 'run', side_effect=command):
                if fails:
                    with self.assertRaises(RuntimeError):
                        ipsec_nat.edit_rules(ipsec_nat.association(self.env, 20000), False)
                else:
                    ipsec_nat.edit_rules(ipsec_nat.association(self.env, 20000), False)

    def retired(self):
        a = ipsec_nat.association(self.env, 20000)
        a['retired_at'] = 0
        self.state.update(next=65536, retired={'20000': a})
        return a

    def test_recycle_requires_quarantine_and_two_empty_observations(self):
        self.retired()
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'l2tp_peers', return_value=set()), \
                patch.object(ipsec_nat, 'run', return_value=subprocess.CompletedProcess([], 0, '', '')), \
                patch.object(ipsec_nat.time, 'monotonic', return_value=29) as clock:
            ipsec_nat.reclaim(self.state)
            self.assertNotIn('quiet_since', self.state['retired']['20000'])
            clock.return_value = 30
            ipsec_nat.reclaim(self.state)
            self.assertFalse(self.state['free'])
            clock.return_value = 31
            ipsec_nat.reclaim(self.state)
            self.assertFalse(self.state['free'])
            clock.return_value = 32
            ipsec_nat.reclaim(self.state)
        self.assertEqual(self.state['free'], [20000])
        self.assertFalse(self.state['retired'])
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'edit_rules'):
            ipsec_nat.update(self.state, self.env)
        self.assertEqual(self.state['next'], 65536)
        self.assertEqual(next(iter(self.state['active'].values()))['slot'], 20000)

    def test_live_state_and_failed_observation_reset_quiet_period(self):
        for reason in ('lns', 'socket', 'queue', 'send_queue', 'xfrm', 'conntrack', 'error', 'bad_ss'):
            with self.subTest(reason=reason):
                a = self.retired()
                a['quiet_since'] = 31
                def command(*args, **kwargs):
                    output = ''
                    if reason == 'error':
                        raise RuntimeError('inspection denied')
                    if args[0] == 'ss':
                        output = {'socket': 'ESTAB 0 0 198.18.99.1:1701 192.0.2.2:20000',
                                  'queue': 'UNCONN 10 0 0.0.0.0:1701 0.0.0.0:*',
                                  'send_queue': 'UNCONN 0 10 0.0.0.0:1701 0.0.0.0:*',
                                  'bad_ss': 'unexpected'}.get(reason, '')
                    elif args[0] == 'ip' and reason == 'xfrm':
                        output = 'reqid 1 mode transport'
                    elif args[0] == 'conntrack' and reason == 'conntrack':
                        output = 'udp 17 20 src=192.0.2.2 dst=198.18.99.1'
                    return subprocess.CompletedProcess(args, 0, output, '')
                with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat.time, 'monotonic', return_value=40), \
                        patch.object(ipsec_nat, 'l2tp_peers', return_value={('192.0.2.2', 20000)} if reason == 'lns' else set()), \
                        patch.object(ipsec_nat, 'run', side_effect=command):
                    ipsec_nat.reclaim(self.state)
                self.assertFalse(self.state['free'])
                self.assertNotIn('quiet_since', a)

    def test_other_peers_and_reqid_prefix_do_not_prevent_recycling(self):
        a = self.retired()
        with patch.object(ipsec_nat, 'run', return_value=subprocess.CompletedProcess([], 0, '', '')):
            self.assertTrue(ipsec_nat.unused_endpoint(a, {('192.0.2.2', 20001)},
                'ESTAB 0 0 198.18.99.1:1701 192.0.2.2:20001', 'reqid 10 mode transport'))

    def test_full_pool_completes_second_observation_in_first_up_hook(self):
        self.retired()
        clock = [30]
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'edit_rules'), \
                patch.object(ipsec_nat, 'l2tp_peers', return_value=set()), \
                patch.object(ipsec_nat, 'run', return_value=subprocess.CompletedProcess([], 0, '', '')), \
                patch.object(ipsec_nat.time, 'monotonic', lambda: clock[0]), \
                patch.object(ipsec_nat.time, 'sleep', lambda seconds: clock.__setitem__(0, clock[0] + seconds)):
            ipsec_nat.update(self.state, self.env)
        self.assertEqual(next(iter(self.state['active'].values()))['slot'], 20000)
        self.assertEqual(self.state['next'], 65536)
        self.assertEqual(clock[0], 32)

    def test_bounded_collection_rotates_past_busy_endpoints(self):
        self.state['retired'] = {}
        for slot in range(20000, 20000 + ipsec_nat.RECLAIM_BATCH + 1):
            a = ipsec_nat.association(self.env, slot)
            a['retired_at'] = 0
            self.state['retired'][str(slot)] = a
        busy = {('192.0.2.2', s) for s in range(20000, 20000 + ipsec_nat.RECLAIM_BATCH)}
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'l2tp_peers', return_value=busy), \
                patch.object(ipsec_nat, 'run', return_value=subprocess.CompletedProcess([], 0, '', '')), \
                patch.object(ipsec_nat.time, 'monotonic', return_value=30) as clock:
            ipsec_nat.reclaim(self.state)
            self.assertFalse(self.state['free'])
            ipsec_nat.reclaim(self.state)
            self.assertFalse(self.state['free'])
            clock.return_value = 32
            ipsec_nat.reclaim(self.state)
        self.assertEqual(self.state['free'], [20000 + ipsec_nat.RECLAIM_BATCH])

    def test_late_queue_activity_restarts_quiet_observation(self):
        a = self.retired()
        a['quiet_since'] = 31
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'l2tp_peers', return_value=set()), \
                patch.object(ipsec_nat, 'run', return_value=subprocess.CompletedProcess([], 0, '', '')), \
                patch.object(ipsec_nat, 'unused_endpoint', side_effect=[True, False]), \
                patch.object(ipsec_nat.time, 'monotonic', return_value=35):
            ipsec_nat.reclaim(self.state)
        self.assertFalse(self.state['free'])
        self.assertNotIn('quiet_since', a)

    def test_partial_teardown_keeps_recoverable_active_journal(self):
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'edit_rules'):
            ipsec_nat.update(self.state, self.env)
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'edit_rules', side_effect=RuntimeError('permission')):
            with self.assertRaises(RuntimeError):
                ipsec_nat.update(self.state, dict(self.env, PLUTO_VERB='down-host'))
        self.assertEqual(len(self.state['active']), 1)
        self.assertFalse(self.state['retired'])
        self.assertFalse(self.state['free'])

    def test_profile_and_tunnel_status_must_be_complete(self):
        available = '00 OK\n00 AVAILABLE lns.0.name=default\n00 AVAILABLE lns.count=1\n00 AVAILABLE lac.count=0\n00 AVAILABLE tunnel 0xabcd, id 15, ourtid 9 has 0 calls and self 0xabc1\n00 AVAILABLE tunnels count=1\n00 AVAILABLE calls count=0\n'
        status = '00 OK\n00 STATUS tunnels.0.id=15\n00 STATUS tunnels.0.peer=192.0.2.2:20000\n00 STATUS tunnels.0.calls.count=0\n00 STATUS tunnels.count=1\n'
        with patch.object(ipsec_nat, 'control_status', side_effect=[available, status, available]):
            self.assertEqual(ipsec_nat.l2tp_peers(), {('192.0.2.2', 20000)})
        default_lac = available.replace('lac.count=0\n', 'lac.0.name=\n00 AVAILABLE lac.count=1\n')
        with patch.object(ipsec_nat, 'control_status', side_effect=[default_lac, status, default_lac]):
            self.assertEqual(ipsec_nat.l2tp_peers(), {('192.0.2.2', 20000)})
        for a, s in ((available, status.rsplit('00 STATUS tunnels.count', 1)[0]),
                     (available, status.replace('calls.count=0', 'calls.count=1')),
                     (available, status.replace('tunnels.count=1', 'tunnels.count=0')),
                     (available, status + '00 STATUS tunnels.count=1\n'),
                     (available.replace('lac.count=0', 'lac.count=1'), status),
                     (available.replace('lns.count=1', 'lns.count=2'), status),
                     (available, '01 Error\n')):
            with self.subTest(a=a, s=s), patch.object(ipsec_nat, 'control_status', side_effect=[a, s, a]):
                with self.assertRaises((KeyError, RuntimeError)):
                    ipsec_nat.l2tp_peers()

    def test_control_reply_larger_than_pipe_buffer_is_complete(self):
        if os.geteuid() != 0:
            self.skipTest('control pipe ownership requires root')
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            fifo = root / 'control'
            os.mkfifo(fifo, 0o600)
            listener = os.open(fifo, os.O_RDWR | os.O_NONBLOCK)
            response = '00 OK\n' + '00 STATUS tunnels.0.id=1\n' * 6000
            def responder():
                while True:
                    try:
                        command = os.read(listener, 512).decode()
                    except BlockingIOError:
                        time.sleep(.01)
                        continue
                    reply = Path(command.split(' ')[0][1:])
                    with reply.open('w') as f:
                        f.write(response[:4000])
                        f.flush()
                        time.sleep(.05)
                        f.write(response[4000:])
                    reply.unlink()
                    return
            thread = threading.Thread(target=responder, daemon=True)
            thread.start()
            try:
                with patch.object(ipsec_nat, 'ROOT', root), patch.object(ipsec_nat, 'CONTROL', fifo):
                    self.assertEqual(ipsec_nat.control_status('y default'), response)
                thread.join(2)
                self.assertFalse(thread.is_alive())
            finally:
                os.close(listener)
            self.assertEqual(list(root.glob('query-*')), [])

    def test_global_census_detects_half_open_and_changing_tunnels(self):
        prefix = '00 OK\n00 AVAILABLE lns.0.name=default\n00 AVAILABLE lns.count=1\n00 AVAILABLE lac.count=0\n'
        empty = prefix + '00 AVAILABLE tunnels count=0\n00 AVAILABLE calls count=0\n'
        half = prefix + '00 AVAILABLE tunnel 0xabcd, id 0, ourtid 12 has 0 calls and self 0xabc1\n00 AVAILABLE tunnels count=1\n00 AVAILABLE calls count=0\n'
        status = '00 OK\n00 STATUS tunnels.count=0\n'
        for before, after in ((half, half), (empty, half)):
            with self.subTest(before=before), patch.object(ipsec_nat, 'control_status', side_effect=[before, status, after]):
                with self.assertRaisesRegex(RuntimeError, 'unassigned'):
                    ipsec_nat.l2tp_peers()
        for response in (prefix, empty.replace('tunnels count=0', 'tunnels count=1'),
                         half.replace('00 AVAILABLE calls count=0\n', '')):
            with self.subTest(response=response), self.assertRaises(RuntimeError):
                ipsec_nat.available_status(response)

    def test_xfrm_reqid_reuse_is_distinguished_by_generation_marks(self):
        a = self.retired()
        for mark, busy in [('0x1/0xffffffff', True), ('0x2/0xffffffff', False),
                           ('0x101/0xff', True), ('invalid', True)]:
            with self.subTest(mark=mark):
                dump = f'src 192.0.2.2 dst 198.18.99.1\n proto esp reqid 1 mode transport\n mark {mark}\n'
                self.assertEqual(ipsec_nat.xfrm_in_use(a, dump), busy)
        self.assertFalse(ipsec_nat.xfrm_in_use(a, 'src a dst b\n reqid 10\n mark 0x1/0xffffffff\n'))
        self.assertTrue(ipsec_nat.xfrm_in_use(a,
            'src a dst b\n reqid 1\n mark 0x2/0xffffffff\nsrc c dst d\n reqid 1\n mark 0x1/0xffffffff\n'))

    def test_unmarked_inbound_spis_distinguish_retired_and_new_generations(self):
        a = self.retired()
        old = 'src 192.0.2.2 dst 198.18.99.1\n proto esp spi 0x100 reqid 1 mode transport\n auth-trunc hmac(sha256) fixture-key 128\n'
        new = old.replace('0x100', '0x200')
        marked = old.replace('0x100', '0x300') + ' mark 0x2/0xffffffff\n'
        with patch.object(ipsec_nat, 'run', return_value=subprocess.CompletedProcess([], 0, old + marked, '')):
            a['unmarked_spis'] = self.original_capture(a)
        self.assertEqual(a['unmarked_spis'], ['192.0.2.2|198.18.99.1|256'])
        self.assertNotIn('fixture-key', json.dumps(a))
        self.assertTrue(ipsec_nat.xfrm_in_use(a, old))
        self.assertFalse(ipsec_nat.xfrm_in_use(a, new + marked))
        self.assertTrue(ipsec_nat.xfrm_in_use(a, new + ' dir in priority 1\n'))
        del a['unmarked_spis']
        self.assertTrue(ipsec_nat.xfrm_in_use(a, new))

    def test_sa_snapshot_failure_keeps_active_journal_for_retry(self):
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'edit_rules'):
            ipsec_nat.update(self.state, self.env)
            with patch.object(ipsec_nat, 'capture_unmarked', side_effect=RuntimeError('failed')):
                with self.assertRaises(RuntimeError):
                    ipsec_nat.update(self.state, dict(self.env, PLUTO_VERB='down-host'))
        self.assertEqual(len(self.state['active']), 1)
        self.assertFalse(self.state['retired'])

    def test_failed_install_then_duplicate_up_and_final_down_close_both_reservations(self):
        with patch.object(ipsec_nat, 'save'), \
                patch.object(ipsec_nat, 'edit_rules', side_effect=[RuntimeError('failed'), None]):
            with self.assertRaises(RuntimeError):
                ipsec_nat.update(self.state, self.env)
        with patch.object(ipsec_nat, 'save'), patch.object(ipsec_nat, 'edit_rules'):
            ipsec_nat.update(self.state, self.env)
            ipsec_nat.update(self.state, dict(self.env, PLUTO_VERB='down-host'))
        self.assertFalse(self.state['active'])
        self.assertEqual(len(self.state['retired']), 2)
        self.assertTrue(all('unmarked_spis' in a for a in self.state['retired'].values()))

    def test_rule_inspection_failure_does_not_retire_endpoint(self):
        with patch.object(ipsec_nat, 'run', return_value=subprocess.CompletedProcess([], 4, '', 'denied')):
            with self.assertRaisesRegex(RuntimeError, 'inspection failed'):
                ipsec_nat.edit_rules(ipsec_nat.association(self.env, 20000), False)

    def test_concurrent_processes_lock_journal_and_do_not_duplicate_slots(self):
        with tempfile.TemporaryDirectory() as temp:
            # The filesystem journal and flock are real. Kernel mutations are
            # stubbed here; the real dial fixture covers the actual rules.
            code = '''import os,sys,time
sys.path.insert(0,sys.argv[1]);import ipsec_nat as n
n.ROOT=n.Path(sys.argv[2]);sys.argv=['ipsec_nat.py']
n.edit_rules=lambda *a:time.sleep(.01)
n.capture_unmarked=lambda a:[]
n.main()
'''
            def batch(verb):
                jobs = []
                for i in range(16):
                    env = dict(os.environ, **self.env)
                    env.update(PLUTO_VERB=verb, PLUTO_REQID=str(i+1), PLUTO_MARK_IN=f'{i+1}/0xffffffff', PLUTO_MARK_OUT=f'{i+1}/0xffffffff')
                    jobs.append(subprocess.Popen([sys.executable, '-c', code, str(Path(ipsec_nat.__file__).parent), temp], env=env,
                                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE))
                for job in jobs:
                    _, error = job.communicate(timeout=20)
                    self.assertEqual(job.returncode, 0, error.decode())
            batch('up-host')
            state = json.loads((Path(temp) / 'state.json').read_text())
            self.assertEqual(len(state['active']), 16)
            self.assertEqual(len({a['slot'] for a in state['active'].values()}), 16)
            batch('up-host')
            self.assertEqual(json.loads((Path(temp) / 'state.json').read_text())['next'], 20016)
            batch('down-host')
            state = json.loads((Path(temp) / 'state.json').read_text())
            self.assertFalse(state['active'])
            self.assertEqual(len(state['retired']), 16)
            # Move strictly beyond the quarantine boundary. Subtracting two
            # monotonic floats at exactly +30 can round just below 30, leaving
            # the last endpoint needing its first quiet observation next time.
            clock = [max(a['retired_at'] for a in state['retired'].values()) + ipsec_nat.QUARANTINE + 1]
            with patch.object(ipsec_nat, 'ROOT', Path(temp)), \
                    patch.object(ipsec_nat, 'l2tp_peers', return_value=set()), \
                    patch.object(ipsec_nat, 'run', return_value=subprocess.CompletedProcess([], 0, '', '')), \
                    patch.object(ipsec_nat.time, 'monotonic', lambda: clock[0]):
                ipsec_nat.reclaim(state)
                clock[0] += ipsec_nat.QUIET + 1
                ipsec_nat.reclaim(state)
            self.assertEqual(len(state['free']), 16)
            batch('up-host')
            state = json.loads((Path(temp) / 'state.json').read_text())
            self.assertEqual(state['next'], 20016)
            self.assertEqual(len({a['slot'] for a in state['active'].values()}), 16)
            self.assertFalse(state['free'])

    def test_more_than_45536_lifetimes_do_not_exhaust_recycled_pool(self):
        clock = [0]
        with patch.object(ipsec_nat, 'save', lambda *_: None), patch.object(ipsec_nat, 'edit_rules', lambda *_: None), \
                patch.object(ipsec_nat, 'l2tp_peers', lambda: set()), \
                patch.object(ipsec_nat, 'run', lambda *a, **k: subprocess.CompletedProcess(a, 0, '', '')), \
                patch.object(ipsec_nat.time, 'monotonic', lambda: clock[0]):
            for i in range(45540):
                clock[0] = i * 40
                ipsec_nat.update(self.state, self.env)
                ipsec_nat.update(self.state, dict(self.env, PLUTO_VERB='down-host'))
                clock[0] += 30
                ipsec_nat.reclaim(self.state)
                clock[0] += 2
                ipsec_nat.reclaim(self.state)
            self.assertEqual(self.state['next'], 20001)
            self.assertEqual(self.state['free'], [20000])
            self.assertFalse(self.state['active'])
            self.assertFalse(self.state['retired'])


if __name__ == '__main__':
    unittest.main()

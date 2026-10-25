import importlib.util
from pathlib import Path
import subprocess
import unittest
from unittest.mock import Mock, patch


spec = importlib.util.spec_from_file_location('fresh_install_fixture', Path(__file__).resolve().parents[1] / 'test_fresh_install.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class GuestReadinessTests(unittest.TestCase):
    def setUp(self):
        self.now = 0
        self.vm = Mock()
        self.vm.poll.return_value = None
        self.console = Mock()
        self.clock = patch.object(module.time, 'monotonic', side_effect=lambda: self.now)
        self.sleep = patch.object(module.time, 'sleep', side_effect=self.advance)
        self.clock.start()
        self.sleep.start()
        self.addCleanup(self.clock.stop)
        self.addCleanup(self.sleep.stop)

    def advance(self, seconds):
        self.now += seconds

    def test_transient_timeout_and_refused_ssh_are_retried(self):
        guest = Mock(side_effect=[subprocess.TimeoutExpired('ssh', 15),
                                  subprocess.CompletedProcess('ssh', 255, ''),
                                  subprocess.CompletedProcess('ssh', 0, 'new-boot\n')])
        self.assertEqual(module.wait_for_guest(self.vm, guest, self.console), 'new-boot')
        self.assertEqual(guest.call_count, 3)
        self.console.read_text.assert_not_called()

    def test_reboot_requires_changed_boot_id(self):
        guest = Mock(side_effect=[subprocess.CompletedProcess('ssh', 0, 'old-boot\n'),
                                  subprocess.CompletedProcess('ssh', 0, '\n'),
                                  subprocess.CompletedProcess('ssh', 0, 'new-boot\n')])
        self.assertEqual(module.wait_for_guest(self.vm, guest, self.console, previous='old-boot'), 'new-boot')
        self.assertEqual(guest.call_count, 3)

    def test_repeated_timeouts_respect_total_deadline(self):
        timeouts = []
        def unavailable(command, check, timeout):
            timeouts.append(timeout)
            self.advance(timeout)
            raise subprocess.TimeoutExpired('ssh', timeout)
        with self.assertRaisesRegex(RuntimeError, 'guest boot timed out: SSH readiness probe timed out'):
            module.wait_for_guest(self.vm, unavailable, self.console, timeout=20)
        self.assertEqual(timeouts, [15, 2])
        self.assertEqual(self.now, 20)

    def test_exited_vm_and_unexpected_errors_are_not_retried(self):
        self.vm.poll.return_value = 1
        self.console.read_text.return_value = 'fixture boot error'
        guest = Mock()
        with self.assertRaisesRegex(RuntimeError, 'QEMU exited: fixture boot error'):
            module.wait_for_guest(self.vm, guest, self.console)
        guest.assert_not_called()
        self.vm.poll.return_value = None
        guest.side_effect = OSError('SSH executable unavailable')
        with self.assertRaises(OSError):
            module.wait_for_guest(self.vm, guest, self.console)
        guest.assert_called_once()


if __name__ == '__main__':
    unittest.main()

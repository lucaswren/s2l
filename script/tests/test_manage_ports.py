from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import manage_config
import manage_ssh


class PortTests(unittest.TestCase):
    def test_port_boundaries(self):
        for value in ("1", "65535", "00022"):
            self.assertEqual(manage_config.validate_port(value), int(value))
        for value in ("0", "65536", "-1", "abc", "22\n", "123456"):
            with self.assertRaises(ValueError):
                manage_config.validate_port(value)

    def test_random_port_retries_occupied_port(self):
        with patch.object(manage_config.secrets, "randbelow", side_effect=[1, 2]), patch.object(manage_config, "check_available_port", side_effect=[OSError(), None]):
            self.assertEqual(manage_config.random_port(), 10002)

    def test_web_port_preserves_ipv6_address_and_credentials(self):
        original = dict(listen="[::1]:8080", admin_user="admin", admin_pass="a" * 12, data_dir="/opt/s2l/data")
        updated = manage_config.update_config(original.copy(), "listen_port", "40001")
        self.assertEqual(updated["listen"], "[::1]:40001")
        self.assertEqual(updated["admin_pass"], original["admin_pass"])
        self.assertEqual(updated["data_dir"], original["data_dir"])

    def test_rewrite_ports_preserves_other_settings(self):
        source = b"# Port 22\nPort 22\nport=2222\nPasswordAuthentication no\nMatch User example\n  X11Forwarding no\n"
        result = manage_ssh.rewrite_ports(source, 40001, True)
        self.assertTrue(result.startswith(b"Port 40001\n"))
        self.assertIn(b"# s2l disabled: Port 22\n", result)
        self.assertIn(b"# s2l disabled: port=2222\n", result)
        self.assertIn(b"PasswordAuthentication no\nMatch User example\n  X11Forwarding no\n", result)

    def test_socket_override_preserves_listen_addresses(self):
        result = manage_ssh.socket_override("port 40001\nlistenaddress 127.0.0.1:40001\nlistenaddress [::1]:40001\n")
        self.assertEqual(result, b"[Socket]\nListenStream=\nListenStream=127.0.0.1:40001\nListenStream=[::1]:40001\n")

    def test_recursive_includes_and_cycles(self):
        main = manage_ssh.SSH_CONFIG
        child = main.parent / "sshd_config.d" / "custom.conf"
        sources = {main: b"Include sshd_config.d/*.conf\n", child: b"Port 2222\nInclude /etc/ssh/sshd_config\n"}
        def expand(pattern):
            return [str(child)] if pattern.endswith("*.conf") else [str(main)]
        with patch.object(Path, "is_symlink", return_value=False), patch.object(Path, "resolve", lambda path: path), patch.object(Path, "read_bytes", lambda path: sources[path]), patch.object(manage_ssh.glob, "glob", side_effect=expand):
            self.assertEqual(manage_ssh.config_files(main), sources)

    def transaction(self, socket_enabled=False, fail=False, bad_address=False):
        writes = {}
        original = b"Port 22\nPasswordAuthentication no\n"
        evaluations = ["port 22\nlistenaddress 0.0.0.0:22\n", "port 40001\nlistenaddress 0.0.0.0:" + ("22" if bad_address else "40001") + "\n"]
        def run(*args):
            if "-T" in args:
                return evaluations.pop(0)
            if "KillMode" in args:
                return "process\n"
            return ""
        def write(path, data):
            writes[path] = data
        effects = [subprocess.CalledProcessError(1, ["systemctl"]), None] if fail else [None]
        with patch.object(manage_ssh, "run", side_effect=run), patch.object(manage_ssh, "active", side_effect=lambda unit: socket_enabled if unit == "ssh.socket" else unit == "ssh.service"), patch.object(manage_ssh, "check_available_port"), patch.object(manage_ssh, "config_files", return_value={manage_ssh.SSH_CONFIG: original}), patch.object(manage_ssh, "write_atomic", side_effect=write), patch.object(manage_ssh, "apply_runtime", side_effect=effects) as runtime, patch.object(manage_ssh, "listening", return_value=True), patch.object(Path, "is_symlink", return_value=False), patch.object(Path, "exists", return_value=False), patch.object(Path, "mkdir"), patch.object(Path, "unlink") as unlink:
            if fail or bad_address:
                with self.assertRaises(RuntimeError):
                    manage_ssh.change_port("40001")
                self.assertEqual(writes[manage_ssh.SSH_CONFIG], original)
                self.assertEqual(runtime.call_count, 2 if fail else 0)
                if socket_enabled:
                    unlink.assert_called()
            else:
                manage_ssh.change_port("40001")
                self.assertTrue(writes[manage_ssh.SSH_CONFIG].startswith(b"Port 40001\n"))
                runtime.assert_called_once_with("ssh.service", socket_enabled)
                if socket_enabled:
                    self.assertIn(b"ListenStream=0.0.0.0:40001", writes[manage_ssh.SOCKET_CONFIG])

    def test_regular_service_change(self):
        self.transaction()

    def test_socket_service_change(self):
        self.transaction(socket_enabled=True)

    def test_restart_failure_restores_old_config(self):
        self.transaction(fail=True)

    def test_socket_failure_removes_new_override(self):
        self.transaction(socket_enabled=True, fail=True)

    def test_explicit_listen_address_rolls_back_before_reload(self):
        self.transaction(bad_address=True)


if __name__ == "__main__":
    unittest.main()

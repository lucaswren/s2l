from pathlib import Path
import json
import sys
import tempfile
import shutil
import ssl
import subprocess
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import verify_update


class UpdateVerificationTests(unittest.TestCase):
    @unittest.skipUnless(shutil.which('openssl'), 'requires openssl TLS fixture')
    def test_https_retries_backend_and_pins_certificate_on_loopback(self):
        class Handler(BaseHTTPRequestHandler):
            statuses = [502, 200]
            hosts = []
            body = b'{"authenticated":false,"enabled":true}'
            def do_GET(self):
                type(self).hosts.append(self.headers.get('Host'))
                self.send_response(self.statuses.pop(0) if self.statuses else 200)
                self.end_headers()
                self.wfile.write(self.body)
            def log_message(self, *args):
                pass
        with tempfile.TemporaryDirectory() as root:
            cert, key = Path(root)/'cert.pem', Path(root)/'key.pem'
            subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                            '-subj', '/CN=203.0.113.9', '-keyout', str(key), '-out', str(cert)],
                           check=True, capture_output=True, timeout=30)
            server = HTTPServer(('127.0.0.1', 0), Handler)
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.load_cert_chain(cert, key)
            server.socket = context.wrap_socket(server.socket, server_side=True)
            thread = threading.Thread(target=server.serve_forever)
            thread.start()
            try:
                config = {'public_url': f'https://203.0.113.9:{server.server_port}'}
                verify_update.check_https(config, cert, attempts=2, delay=0)
                self.assertEqual(Handler.hosts, [f'203.0.113.9:{server.server_port}'] * 2)
                with patch.object(verify_update.ssl, 'PEM_cert_to_DER_cert', return_value=b'old cert'):
                    with self.assertRaisesRegex(ValueError, 'HTTPS'):
                        verify_update.check_https(config, cert, attempts=1)
                self.assertEqual(len(Handler.hosts), 2, 'must reject wrong certificate before sending HTTP')
                Handler.body = b'{}'
                with self.assertRaisesRegex(ValueError, 'HTTPS'):
                    verify_update.check_https(config, cert, attempts=1)
                Handler.statuses = [302]
                with self.assertRaisesRegex(ValueError, 'HTTP 302'):
                    verify_update.check_https(config, cert, attempts=1)
            finally:
                server.shutdown(); thread.join(); server.server_close()

    @unittest.skipUnless(sys.platform.startswith("linux"), "Linux managed paths")
    def test_rejects_external_backend_and_custom_state(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "config.json"
            path.write_text('{"listen":"127.0.0.1:8080"}')
            self.assertEqual(verify_update.preflight(path)["listen"], "127.0.0.1:8080")
        for config in ({"listen": "203.0.113.1:8080"}, {"data_dir": "/different"}):
            with tempfile.TemporaryDirectory() as root:
                path = Path(root) / "config.json"
                path.write_text(json.dumps(config))
                with self.assertRaises(ValueError):
                    verify_update.preflight(path)

    def test_local_health_authentication_and_new_schema(self):
        class Handler(BaseHTTPRequestHandler):
            modern = True
            redirect = False
            omit_cookie = False
            expire_once = False
            logouts = 0
            def do_POST(self):
                if self.headers.get("Authorization") is not None:
                    self.send_response(400)
                    self.end_headers()
                    return
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
                if self.path == "/api/auth/logout":
                    type(self).logouts += 1
                    self.send_response(200)
                    self.end_headers()
                    return
                if self.path != "/api/auth/login" or body != {"username": "admin", "password": "secret-password"}:
                    self.send_response(401)
                    self.end_headers()
                    return
                self.send_response(200)
                if not self.omit_cookie:
                    self.send_header("Set-Cookie", "s2l_session=test-session; Path=/; HttpOnly; SameSite=Strict")
                self.end_headers()
                self.wfile.write(b'{"authenticated":true}')
            def do_GET(self):
                if self.path != "/api/status" or self.headers.get("Cookie") != "s2l_session=test-session" or self.headers.get("Authorization") is not None or self.expire_once:
                    type(self).expire_once = False
                    self.send_response(401)
                    self.end_headers()
                    return
                if self.redirect:
                    self.send_response(302)
                    self.send_header("Location", "http://127.0.0.1:1/")
                    self.end_headers()
                    return
                self.send_response(200)
                self.end_headers()
                payload = {"mappings": [], "startup_complete": True, "singbox_pids": {}}
                if self.modern:
                    payload["upstream_health"] = {}
                self.wfile.write(json.dumps(payload).encode())
            def log_message(self, *args):
                pass
        server = HTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        try:
            config = dict(listen=f"127.0.0.1:{server.server_port}", admin_user="admin", admin_pass="secret-password")
            with patch.dict("os.environ", {"http_proxy": "http://127.0.0.1:1"}):
                verify_update.check_health(config, attempts=1)
            self.assertEqual(Handler.logouts, 1)
            Handler.expire_once = True
            verify_update.check_health(config, attempts=2, delay=0)
            Handler.omit_cookie = True
            with self.assertRaises(ValueError):
                verify_update.check_health(config, attempts=1)
            Handler.omit_cookie = False
            Handler.redirect = True
            with self.assertRaises(ValueError):
                verify_update.check_health(config, attempts=1)
            Handler.redirect = False
            Handler.modern = False
            with self.assertRaises(ValueError):
                verify_update.check_health(config, attempts=1)
            Handler.modern = True
            config["admin_pass"] = "incorrect"
            with self.assertRaises(ValueError):
                verify_update.check_health(config, attempts=1)
        finally:
            server.shutdown()
            thread.join()
            server.server_close()


class LocalReadinessTests(unittest.TestCase):
    def test_expected_mapping_must_be_locally_ready_but_proxy_may_be_offline(self):
        status = {"startup_complete": False, "mappings": [{"id": "m", "status": "running", "tun_name": "tun101"}], "upstream_health": {"m": {"local_state": "healthy", "state": "unavailable"}}, "singbox_pids": {"tun101": 123}}
        self.assertFalse(verify_update.locally_ready(status, ["m"]))
        status["startup_complete"] = True
        self.assertTrue(verify_update.locally_ready(status, ["m"]))
        self.assertFalse(verify_update.locally_ready(status, ["missing"]))
        status["singbox_pids"] = {}
        self.assertFalse(verify_update.locally_ready(status, ["m"]))
        self.assertTrue(verify_update.locally_ready(status, ["m"], auto_restore=False))
        status["singbox_pids"] = {"tun101": 123}
        status["upstream_health"]["m"]["local_state"] = "recovering"
        self.assertFalse(verify_update.locally_ready(status, ["m"]))

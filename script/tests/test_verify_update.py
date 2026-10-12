from pathlib import Path
import base64
import json
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import verify_update


class UpdateVerificationTests(unittest.TestCase):
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
        expected = "Basic " + base64.b64encode(b"admin:secret-password").decode()
        class Handler(BaseHTTPRequestHandler):
            modern = True
            redirect = False
            def do_GET(self):
                if self.path != "/api/status" or self.headers.get("Authorization") != expected:
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
                payload = {"mappings": []}
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

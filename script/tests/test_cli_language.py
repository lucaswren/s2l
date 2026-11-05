import contextlib
import http.server
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch

sys.path.append(str(Path(__file__).resolve().parents[1]))
import installer_i18n
import manage_config

SCRIPTS = Path(__file__).resolve().parents[1]


class CLILanguageTests(unittest.TestCase):
    def test_catalog_reads_web_preference_and_never_exposes_credentials(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'config.json'
            for saved, expected in [('en', 'en'), ('zh-TW', 'zh-Hant'), ('zh-CN', 'zh-Hans'), ('invalid', 'en'), ([], 'en')]:
                path.write_text(json.dumps(dict(ui_language=saved, admin_pass='fixture-secret-never-export')))
                catalog = installer_i18n.cli_catalog(path)
                self.assertEqual(catalog['_language'], expected)
                self.assertNotIn('fixture-secret-never-export', json.dumps(catalog))
            path.write_text('invalid JSON')
            self.assertEqual(installer_i18n.cli_catalog(path)['_language'], 'en')
            self.assertEqual(installer_i18n.cli_catalog(path.with_name('missing'))['_language'], 'en')

    def test_shared_labels_match_web_catalog(self):
        catalog = json.loads((SCRIPTS.parent / 'web/src/i18n/messages.json').read_text(encoding='utf-8'))
        for key in ('软件更新', '管理用户名', 'SSH 端口'):
            self.assertEqual(installer_i18n.MESSAGES[key][:2], (catalog[key]['en'], catalog[key]['tw']))

    def test_invalid_language_and_external_listener_never_send_credentials(self):
        with patch.object(manage_config, 'validate_port') as port:
            for value in ('de', 'en;id', '../config', ''):
                with self.assertRaises(ValueError):
                    manage_config.change_language({}, value)
            with self.assertRaises(ValueError):
                manage_config.change_language({'listen': '203.0.113.5:8080'}, 'en')
            port.assert_not_called()


@contextlib.contextmanager
def local_api(config_path, acknowledge=True):
    requests = []

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def reply(self, body, cookie=False):
            data = json.dumps(body).encode()
            self.send_response(200)
            self.send_header('Content-Length', str(len(data)))
            if cookie:
                self.send_header('Set-Cookie', 's2l_session=fixture; Path=/; HttpOnly')
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            requests.append(self.path)
            self.reply({'language': json.loads(config_path.read_text()).get('ui_language', '')})

        def do_POST(self):
            requests.append(self.path)
            body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', 0))))
            if self.path == '/api/auth/login':
                self.reply({'authenticated': True}, cookie=True)
            elif self.path == '/api/settings/language':
                if self.headers.get('Cookie') != 's2l_session=fixture':
                    self.send_error(401)
                    return
                if acknowledge:
                    config = json.loads(config_path.read_text())
                    config['ui_language'] = body['language']
                    config_path.write_text(json.dumps(config))
                    self.reply({'language': body['language']})
                else:
                    self.reply({'language': 'unconfirmed'})
            else:
                self.reply({'authenticated': False})

    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        config_path.write_text(json.dumps(dict(listen='127.0.0.1:' + str(server.server_port),
                                               admin_user='admin', admin_pass='fixture-password', ui_language='en',
                                               custom={'preserve': True})))
        yield requests
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


class CLILanguageAPITests(unittest.TestCase):
    def test_language_uses_authenticated_api_without_restarting_services(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / 'config.json'
            with local_api(config) as requests, patch.object(manage_config, 'restart_service') as restart:
                original = json.loads(config.read_text())
                manage_config.change_language(original, 'zh-TW')
                expected = dict(original, ui_language='zh-TW')
                self.assertEqual(json.loads(config.read_text()), expected)
                self.assertEqual(requests, ['/api/auth/login', '/api/settings/language', '/api/auth/status', '/api/auth/logout'])
                restart.assert_not_called()

    def test_unacknowledged_save_is_an_error_and_session_is_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / 'config.json'
            with local_api(config, acknowledge=False) as requests:
                with self.assertRaises(ValueError):
                    manage_config.change_language(json.loads(config.read_text()), 'zh-TW')
                self.assertEqual(json.loads(config.read_text())['ui_language'], 'en')
                self.assertEqual(requests[-1], '/api/auth/logout')


@unittest.skipUnless(sys.platform == 'linux', 'Linux Bash menu')
class CLILanguageMenuTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config = self.root / 'config.json'
        tools = self.root / 'tools'
        tools.mkdir()
        for name, body in {'id': 'echo 0', 'clear': 'exit 0', 'systemctl': 'touch "$TEST_RESTART"; exit 99'}.items():
            p = tools / name
            p.write_text('#!/bin/sh\n' + body + '\n')
            p.chmod(0o700)
        self.restart = self.root / 'restart'
        self.env = dict(os.environ, PATH=str(tools) + ':' + os.environ['PATH'], TEST_RESTART=str(self.restart))
        wrapper = self.root / 'manage_config.py'
        wrapper.write_text('import sys\nfrom pathlib import Path\nsys.path.insert(0,' + repr(str(SCRIPTS)) + ')\n'
                           'import manage_config\nmanage_config.CONFIG_FILE=Path(' + repr(str(self.config)) + ')\nmanage_config.main()\n')
        source = (SCRIPTS / 's2l.sh').read_text()
        source = source.replace('/opt/s2l/config.json', str(self.config)).replace('/opt/s2l/installer_i18n.py', str(SCRIPTS / 'installer_i18n.py'))
        source = source.replace('/opt/s2l/manage_config.py', str(wrapper))
        self.script = self.root / 's2l.sh'
        self.script.write_text(source)

    def run_cli(self, *args, input=''):
        return subprocess.run(['bash', str(self.script), *args], input=input, text=True,
                              env=self.env, capture_output=True, timeout=30)

    def test_all_three_menu_languages_and_existing_action_numbers(self):
        for saved, title, language in [('en', 'S2L management', 'Language'), ('zh-TW', 'S2L 管理', '語言'), ('zh-CN', 'S2L 管理', '语言')]:
            self.config.write_text(json.dumps({'ui_language': saved}))
            result = self.run_cli(input='0\n')
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn(title, result.stdout)
            self.assertIn('14) ' + language, result.stdout)
            for number in range(15):
                self.assertEqual(sum(line.startswith(str(number) + ') ') for line in result.stdout.splitlines()), 1)
            help_result = self.run_cli('--help')
            self.assertEqual(help_result.returncode, 0)
            self.assertIn('s2l language [en|zh-Hant|zh-Hans]', help_result.stdout)
        self.assertFalse(self.restart.exists())

    def test_cli_language_command_saves_web_setting_without_service_actions(self):
        with local_api(self.config):
            for value, saved, message in [('zh-Hant', 'zh-TW', '語言已儲存'), ('zh-Hans', 'zh-CN', '语言设置已保存'), ('en', 'en', 'Language saved')]:
                result = self.run_cli('language', value)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(message, result.stdout)
                self.assertEqual(json.loads(self.config.read_text())['ui_language'], saved)
                self.assertNotIn('fixture-password', result.stdout + result.stderr)
        self.assertFalse(self.restart.exists())

    def test_invalid_language_arguments_are_read_only(self):
        self.config.write_text('{"ui_language":"en"}')
        before = self.config.read_bytes()
        for args in [('language', 'de'), ('language', 'en;id'), ('14', '--yes'), ('13', '--yes')]:
            self.assertEqual(self.run_cli(*args).returncode, 2)
        self.assertEqual(self.config.read_bytes(), before)
        self.assertFalse(self.restart.exists())

    def test_open_menu_reloads_a_language_changed_by_web(self):
        self.config.write_text('{"ui_language":"en"}')
        process = subprocess.Popen(['bash', str(self.script)], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, text=True, env=self.env)
        try:
            first = ''
            while '0) Exit' not in first:
                line = process.stdout.readline()
                self.assertTrue(line, 'menu closed before becoming ready')
                first += line
            self.config.write_text('{"ui_language":"zh-TW"}')
            output, errors = process.communicate('99\n\n0\n', timeout=30)
            self.assertEqual(process.returncode, 0, errors)
            self.assertIn('S2L management', first)
            self.assertIn('14) 語言', output)
            self.assertFalse(self.restart.exists())
        finally:
            if process.poll() is None:
                process.kill()
                process.communicate(timeout=10)

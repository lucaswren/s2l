import contextlib
import io
import os
from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import installer_i18n
import manage_kernel

SCRIPT = Path(__file__).resolve().parents[1]


class InstallerLanguageTests(unittest.TestCase):
    def test_default_helpers_keep_original_messages(self):
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(installer_i18n.tr('requires root'), 'requires root')

    def test_translations_preserve_format_fields_and_external_details(self):
        import string
        parser = string.Formatter()
        for original, translations in installer_i18n.MESSAGES.items():
            fields = sorted(field for _, field, _, _ in parser.parse(original) if field is not None)
            self.assertEqual(len(translations), 3)
            for language, translation in zip(('en', 'zh-Hant', 'zh-Hans'), translations):
                self.assertTrue(translation.strip())
                self.assertEqual(sorted(field for _, field, _, _ in parser.parse(translation) if field is not None), fields)
                with patch.dict(os.environ, _S2L_INSTALL_LANG=language):
                    self.assertEqual(installer_i18n.tr(original), translation)
                    self.assertEqual(installer_i18n.tr('external command detail / secret-value'), 'external command detail / secret-value')

    def test_kernel_exit_20_and_reboot_notice_follow_install_language(self):
        for language, message in [('en', 'manual reboot required'), ('zh-Hant', '等待手動重啟'), ('zh-Hans', '等待人工重启')]:
            with patch.dict(os.environ, _S2L_INSTALL_LANG=language), contextlib.redirect_stdout(io.StringIO()) as output:
                self.assertEqual(manage_kernel.reboot_notice('fixture-kernel', False), 20)
            self.assertIn(message, output.getvalue())
            self.assertIn('sudo reboot', output.getvalue())

    @unittest.skipUnless(sys.platform == 'linux', 'Linux Bash entrypoints')
    def test_help_and_bad_options_exit_before_host_checks(self):
        for script in ('install.sh', 'remote_install.sh'):
            for language, message in [('en', 'Usage:'), ('zh-Hant', '預設'), ('zh-Hans', '默认')]:
                result = subprocess.run(['bash', str(SCRIPT / script), '--help', '--lang', language], text=True, capture_output=True, timeout=5)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(message, result.stdout)
            for args in [('--lang',), ('--lang', 'fr'), ('--lang', 'en', '--lang', 'zh-Hans'), ('--language', 'en')]:
                result = subprocess.run(['bash', str(SCRIPT / script), *args], text=True, capture_output=True, timeout=5)
                self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
            # The piped one-command entrypoint must behave exactly like a file.
            result = subprocess.run(['bash', '-s', '--', '--lang=en', '--help'], input=(SCRIPT / script).read_text() if script == 'remote_install.sh' else '', text=True, capture_output=True, timeout=5) if script == 'remote_install.sh' else None
            if result:
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn('Usage:', result.stdout)

    @unittest.skipUnless(sys.platform == 'linux', 'Linux Bash child propagation')
    def test_selected_language_reaches_python_errors_and_cannot_be_overridden_by_env(self):
        for language, message in [('en', 'Only verified sing-box'), ('zh-Hant', '僅支援已驗證'), ('zh-Hans', '仅支持已验证')]:
            command = 'source "$1/remote_install.sh"; s2l_language --lang "$2"; python3 "$1/install_preflight.py" singbox /bin/echo'
            result = subprocess.run(['bash', '-c', command, 'bash', str(SCRIPT), language], env={**os.environ, '_S2L_INSTALL_LANG': 'invalid', 'S2L_LANG': 'fr'}, capture_output=True, text=True, timeout=5)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(message, result.stderr)
            self.assertNotIn('Traceback', result.stderr)


if __name__ == '__main__':
    unittest.main()

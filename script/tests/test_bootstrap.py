"""Exercise the remote entrypoint against disposable local Git repositories."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / 'remote_install.sh'


@unittest.skipUnless(sys.platform == 'linux', 'Linux Bash bootstrap')
class BootstrapTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.tools = self.root / 'tools'
        self.tools.mkdir()
        for name, body in {
            'id': 'echo 0', 'uname': 'echo Linux', 'systemctl': 'exit 3',
            'apt-get': 'echo unexpected-apt >&2; exit 99',
        }.items():
            path = self.tools / name
            path.write_text('#!/bin/sh\n' + body + '\n')
            path.chmod(0o755)
        # Move host checks/lock to this fixture; all git operations stay local.
        source = SCRIPT.read_text().replace('/run/systemd/system', str(self.root)).replace('/run/s2l-bootstrap.lock', str(self.root / 'lock')).replace('/var/lib/s2l-install', str(self.root / 'journal'))
        self.script = self.root / 'bootstrap.sh'
        self.script.write_text(source)
        self.repo = self.root / 'upstream'
        self.repo.mkdir()
        self.git('init', '-b', 'main', str(self.repo))
        (self.repo / 'script').mkdir()
        (self.repo / 'script/install.sh').write_text('printf "installed:%s:%s\\n" "$1" "$2"\n')
        self.git('-C', str(self.repo), 'add', '.')
        self.git('-C', str(self.repo), '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', 'commit', '-m', 'fixture')
        self.env = {**os.environ, 'PATH': str(self.tools) + ':' + os.environ['PATH'],
                    'SRC_DIR': str(self.root / 'source with spaces'), 'REPO_URL': str(self.repo), 'BRANCH': 'main'}

    def git(self, *args):
        return subprocess.run(['git', *args], check=True, capture_output=True, text=True, timeout=10).stdout

    def run_bootstrap(self, **env):
        return subprocess.run(['bash', str(self.script), '--lang', 'en'], env={**self.env, **env}, capture_output=True, text=True, timeout=10)

    def test_ready_tools_skip_apt_and_language_reaches_install(self):
        for _ in range(2):
            result = self.run_bootstrap()
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn('installed:--lang:en', result.stdout)
            self.assertIn('skipping bootstrap APT', result.stdout)

    def test_dirty_or_different_source_is_not_checked_out(self):
        self.assertEqual(self.run_bootstrap().returncode, 0)
        source = Path(self.env['SRC_DIR'])
        (source / 'untracked').write_text('keep')
        result = self.run_bootstrap()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('local changes', result.stderr)
        self.assertEqual((source / 'untracked').read_text(), 'keep')
        (source / 'untracked').unlink()
        self.git('-C', str(source), 'remote', 'set-url', 'origin', 'https://example.invalid/other.git')
        result = self.run_bootstrap()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('origin differs', result.stderr)

    def test_invalid_source_and_active_tunnel_stop_before_apt(self):
        for changes in ({'BRANCH': '-bad'}, {'BRANCH': ''}, {'SRC_DIR': str(self.root)}, {'SRC_DIR': '/'}):
            result = self.run_bootstrap(**changes)
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn('unexpected-apt', result.stderr)
        (self.tools / 'systemctl').write_text('#!/bin/sh\nexit 0\n')
        result = self.run_bootstrap()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('is active', result.stderr)
        self.assertFalse(Path(self.env['SRC_DIR']).exists())

    def test_interrupted_install_delegates_journal_validation(self):
        (self.tools / 'systemctl').write_text('#!/bin/sh\nexit 0\n')
        journal = self.root / 'journal'
        journal.mkdir()
        (journal / 'journal.json').write_text('{}')
        result = self.run_bootstrap()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('will validate recovery', result.stdout)
        self.assertIn('installed:--lang:en', result.stdout)
        # The bootstrap must not itself interpret or remove journal content.
        self.assertEqual((journal / 'journal.json').read_text(), '{}')

"""Run the piped CLI updater with disposable service/download fixtures."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / 'remote_update.sh'
SHA = 'a' * 40


@unittest.skipUnless(sys.platform == 'linux', 'requires Linux Bash')
class RemoteUpdateTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.app = self.root / 'app'
        self.app.mkdir()
        (self.app / 'config.json').write_text('{}')
        binary = self.app / 's2l'
        binary.write_text('#!/bin/sh\nexit 0\n')
        binary.chmod(0o700)
        self.job = self.root / 'job'
        self.tools = self.root / 'tools'
        self.tools.mkdir()
        self.log = self.root / 'requests'
        self.marker = self.root / 'installed'
        source = self.root / 'source/script'
        source.mkdir(parents=True)
        (source / 'update.sh').write_text('#!/bin/bash\nwhile [[ -z "${S2L_WEB_UPDATE_ID:-}" && ! -f "$TEST_ALLOW" ]]; do sleep .05; done\nprintf \'%s|%s\' "$S2L_VERSION" "$S2L_COMMIT" > "$TEST_MARKER"\nexit "${TEST_BUILD_CODE:-0}"\n')
        archive = self.root / 'source.tgz'
        with tarfile.open(archive, 'w:gz') as tar:
            tar.add(source.parent, arcname='s2l')
        self.env = dict(os.environ, PATH=str(self.tools)+':/usr/bin:/bin', TEST_LOG=str(self.log),
                        TEST_ARCHIVE=str(archive), TEST_MARKER=str(self.marker), TEST_JOB=str(self.job), TEST_ALLOW=str(self.root/'allow'))
        self.env.pop('S2L_REF', None)
        self.env.pop('S2L_COMMIT', None)
        self.env.pop('S2L_VERSION', None)
        self.env.pop('S2L_CLI_UPDATE', None)
        self.env.pop('S2L_WEB_UPDATE_ID', None)
        self.source = SCRIPT.read_text().replace('/opt/s2l', str(self.app)).replace('/var/lib/s2l/cli-update', str(self.job)).replace('/run/s2l-cli-update-launch.lock', str(self.root / 'launch.lock'))
        self.tool('id', '#!/bin/sh\necho 0\n')
        self.tool('systemctl', '#!/bin/sh\nexit 3\n')
        self.tool('curl', "#!/usr/bin/env python3\nimport json,os,pathlib,shutil,sys\nargs=sys.argv[1:];url=next(x for x in args if x.startswith('https://'));out=pathlib.Path(args[args.index('-o')+1])\nwith open(os.environ['TEST_LOG'],'a') as f:f.write(url+'\\n')\nif '/releases/latest' in url:out.write_text(json.dumps({'tag_name':'1.0.2','prerelease':os.environ.get('TEST_BAD_RELEASE')=='1'}))\nelif '/commits/' in url:out.write_text(json.dumps({'sha':'a'*40}))\nelse:shutil.copyfile(os.environ['TEST_ARCHIVE'],out)\n")
        self.tool('systemd-run', "#!/usr/bin/env python3\nimport os,pathlib,subprocess,sys\nif os.environ.get('TEST_LAUNCH_FAIL')=='1':raise SystemExit(7)\nargs=sys.argv[1:];start=args.index('/usr/bin/env');log=pathlib.Path(os.environ['TEST_JOB'])/'update.log'\nwith log.open('ab') as f:p=subprocess.Popen(args[start:],stdin=subprocess.DEVNULL,stdout=f,stderr=f,start_new_session=True,close_fds=True)\n(pathlib.Path(os.environ['TEST_JOB'])/'pid').write_text(str(p.pid))\n")

    def tool(self, name, text):
        p = self.tools / name
        p.write_text(text)
        p.chmod(0o700)

    def launch(self, *args):
        # No source file exists: exercise the documented curl | bash contract.
        return subprocess.run(['bash', '-s', '--', *args], input=self.source, text=True, env=self.env,
                              capture_output=True, start_new_session=True, timeout=10)

    def result(self):
        (self.root/'allow').touch()
        end = time.monotonic() + 10
        while time.monotonic() < end:
            p = self.job / 'result.json'
            if p.exists():
                data = json.loads(p.read_text())
                if data['status'] in ('succeeded', 'failed'):
                    return data
            time.sleep(.05)
        self.fail('worker did not finish: '+(self.job/'update.log').read_text())

    def test_piped_launcher_exits_before_worker_and_pins_stable_identity(self):
        result = self.launch('--detach')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('result.json', result.stdout)
        # The launching shell is gone while the copied runner is still building.
        self.assertFalse(self.marker.exists())
        outcome = self.result()
        self.assertEqual(outcome['status'], 'succeeded')
        self.assertEqual(outcome['version'], '1.0.2')
        self.assertEqual(outcome['commit'], SHA)
        self.assertEqual(self.marker.read_text(), '1.0.2|'+SHA)
        requests = self.log.read_text()
        self.assertIn('/releases/latest', requests)
        self.assertIn('/tar.gz/'+SHA, requests)
        self.assertNotIn('/tar.gz/main', requests)
        self.assertEqual((self.job/'result.json').stat().st_mode & 0o077, 0)

    def test_build_failure_remains_a_persisted_failure(self):
        self.env['TEST_BUILD_CODE'] = '9'
        self.assertEqual(self.launch('--detach').returncode, 0)
        result = self.result()
        self.assertEqual((result['status'], result['exit_code']), ('failed', 9))
        self.assertIn('exit 0', (self.app/'s2l').read_text())

    def test_launch_failure_never_claims_completion(self):
        self.env['TEST_LAUNCH_FAIL'] = '1'
        self.assertNotEqual(self.launch().returncode, 0)
        self.assertEqual(self.result()['status'], 'failed')
        self.assertFalse(self.marker.exists())

    def test_prerelease_is_rejected_before_source_download(self):
        self.env['TEST_BAD_RELEASE'] = '1'
        self.assertEqual(self.launch('--detach').returncode, 0)
        self.assertEqual(self.result()['status'], 'failed')
        self.assertNotIn('/tar.gz/', self.log.read_text())
        self.assertFalse(self.marker.exists())

    def test_web_worker_is_not_nested_and_keeps_its_pinned_identity(self):
        self.env.update(S2L_WEB_UPDATE_ID='fixture', S2L_REF=SHA, S2L_COMMIT=SHA, S2L_VERSION='1.0.2')
        self.assertEqual(self.launch().returncode, 0)
        self.assertFalse(self.job.exists())
        self.assertEqual(self.marker.read_text(), '1.0.2|'+SHA)
        self.assertNotIn('/releases/latest', self.log.read_text())

    def test_foreground_returns_worker_exit_and_can_reopen_result(self):
        (self.root/'allow').touch()
        self.env['TEST_BUILD_CODE'] = '9'
        result = self.launch()
        self.assertEqual(result.returncode, 9, result.stderr)
        self.assertIn('Downloading S2L 1.0.2', result.stdout)
        self.assertIn('Upgrade failed', result.stdout)
        requests = self.log.read_text()
        resumed = self.launch('--follow')
        self.assertEqual(resumed.returncode, 9, resumed.stderr)
        self.assertIn('Upgrade failed', resumed.stdout)
        status = self.launch('--status')
        self.assertEqual(status.returncode, 9, status.stderr)
        self.assertEqual(json.loads(status.stdout)['status'], 'failed')
        self.assertEqual(self.log.read_text(), requests)

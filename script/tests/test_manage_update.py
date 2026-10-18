import contextlib
import copy
import importlib.util
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('manage_update', Path(__file__).resolve().parents[1] / 'manage_update.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
COMMIT = 'a' * 40


class WebUpdateTests(unittest.TestCase):
    def setUp(self):
        self.disk = {}
        self.patches = [patch.object(module, 'read', side_effect=lambda name: copy.deepcopy(self.disk.get(name, {}))),
                        patch.object(module, 'save', side_effect=lambda name, value: self.disk.update({name: copy.deepcopy(value)})),
                        patch.object(module, 'lock', side_effect=lambda *a, **k: contextlib.nullcontext())]
        for item in self.patches:
            item.start()
            self.addCleanup(item.stop)

    def release(self):
        self.disk['release.json'] = {'tag': 'v1.3.0', 'commit': COMMIT, 'checked_epoch': 100}
        return {'tag': 'v1.3.0', 'commit': COMMIT}

    def test_check_pins_commit_and_ignores_external_download_urls(self):
        with patch.object(module, 'job_status', return_value={}), patch.object(module, 'github', side_effect=[{'tag_name': 'v1.3.0', 'tarball_url': 'https://untrusted.invalid'}, {'sha': COMMIT}]) as github:
            result = module.check()
        self.assertEqual(result['commit'], COMMIT)
        self.assertEqual(github.call_args_list[1].args, ('commits/v1.3.0',))
        self.assertEqual(result['url'], 'https://github.com/lucaswren/s2l/releases/tag/v1.3.0')

    def test_prerelease_and_bad_tags_never_launch_or_cache(self):
        for value in ({'tag_name': 'v1.3.0', 'prerelease': True}, {'tag_name': '--help'}, {'tag_name': 'v1.3.0;id'}):
            with patch.object(module, 'job_status', return_value={}), patch.object(module, 'github', return_value=value), self.assertRaises(RuntimeError):
                module.check()
        self.assertFalse(self.disk)

    def test_start_detaches_from_service_and_persists_before_launch(self):
        body = self.release()
        def launch(*args):
            self.assertEqual(self.disk['job.json']['status'], 'queued')
            self.assertEqual(args[0], 'systemd-run')
            self.assertIn('--collect', args)
            self.assertIn('--property=StandardOutput=null', args)
            self.assertIn('--property=StandardError=null', args)
            self.assertIn('--unit=s2l-web-update.service', args)
        with patch.object(module, 'preflight'), patch.object(module, 'unit_active', return_value=False), patch.object(module, 'job_status', return_value={}), patch.object(module.time, 'time', return_value=101), patch.object(module, 'command', side_effect=launch):
            job = module.start(body)
        self.assertEqual(job['commit'], COMMIT)
        self.assertEqual(job['status'], 'queued')

    def test_stale_preview_or_parallel_update_cannot_launch(self):
        body = self.release()
        with patch.object(module, 'preflight'), patch.object(module, 'unit_active', return_value=False), patch.object(module, 'job_status', return_value={}), patch.object(module, 'command') as launch:
            for value, timestamp in (({**body, 'commit': 'b' * 40}, 101), (body, 1001), (body, 99)):
                with patch.object(module.time, 'time', return_value=timestamp), self.assertRaises(RuntimeError):
                    module.start(value)
            launch.assert_not_called()
        with patch.object(module, 'preflight'), patch.object(module, 'unit_active', return_value=True), patch.object(module, 'command') as launch, self.assertRaises(RuntimeError):
            module.start(body)
        launch.assert_not_called()

    def test_failed_launch_is_terminal_and_does_not_run_updater(self):
        body = self.release()
        with patch.object(module, 'preflight'), patch.object(module, 'unit_active', return_value=False), patch.object(module, 'job_status', return_value={}), patch.object(module.time, 'time', return_value=101), patch.object(module, 'command', side_effect=RuntimeError('start failed')), self.assertRaises(RuntimeError):
            module.start(body)
        self.assertEqual(self.disk['job.json']['status'], 'failed')

    def test_worker_runs_exact_approved_commit_and_suppresses_output(self):
        self.disk['job.json'] = {'id': 'job', 'status': 'queued', 'commit': COMMIT, 'tag': 'v1.3.0'}
        with patch.object(module.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0)) as run:
            module.run('job')
        self.assertEqual(run.call_args.kwargs['env']['S2L_REF'], COMMIT)
        self.assertEqual(run.call_args.kwargs['env']['S2L_VERSION'], 'v1.3.0')
        self.assertEqual(run.call_args.kwargs['stdout'], subprocess.DEVNULL)
        self.assertEqual(run.call_args.kwargs['stderr'], subprocess.DEVNULL)
        self.assertEqual(self.disk['job.json']['status'], 'succeeded')

    def test_worker_failure_is_persisted_and_fails_the_unit(self):
        self.disk['job.json'] = {'id': 'job', 'status': 'queued', 'commit': COMMIT, 'tag': 'v1.3.0'}
        with patch.object(module.subprocess, 'run', return_value=subprocess.CompletedProcess([], 1)), self.assertRaises(RuntimeError):
            module.run('job')
        self.assertEqual(self.disk['job.json']['status'], 'failed')
        self.assertEqual(self.disk['job.json']['exit_code'], 1)

    def test_foreign_worker_and_phase_cannot_replace_active_job(self):
        self.disk['job.json'] = {'id': 'current', 'status': 'running', 'phase': 'building'}
        module.phase('old', 'installing')
        self.assertEqual(self.disk['job.json']['phase'], 'building')
        with self.assertRaises(RuntimeError), patch.object(module.subprocess, 'run') as run:
            module.run('old')
        run.assert_not_called()

    def test_interrupted_job_is_reported_and_systemd_errors_fail_closed(self):
        self.disk['job.json'] = {'id': 'current', 'status': 'running', 'started_epoch': 1}
        with patch.object(module, 'unit_active', return_value=False):
            self.assertEqual(module.job_status()['status'], 'interrupted')
        with patch.object(module.subprocess, 'run', return_value=subprocess.CompletedProcess([], 1, '', 'DBus unavailable')), self.assertRaises(RuntimeError):
            module.unit_active()

    def test_default_go_installation_is_detected_without_login_shell_path(self):
        import types
        verifier = types.SimpleNamespace(preflight=lambda path: {})
        with patch.object(module.shutil, 'which', side_effect=lambda name: None if name == 'go' else '/usr/bin/' + name), patch.object(module.os, 'access', return_value=True), patch.object(Path, 'is_symlink', return_value=False), patch.object(Path, 'is_file', return_value=True), patch.dict('sys.modules', {'verify_update': verifier}):
            module.preflight()


if __name__ == '__main__':
    unittest.main()

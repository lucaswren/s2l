#!/usr/bin/env python3
"""Detached web updates. Persist only bounded job state, never command output."""
import contextlib
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
from urllib.request import Request, build_opener, ProxyHandler, HTTPRedirectHandler
import uuid

ROOT = Path('/var/lib/s2l/web-update')
APP = Path('/opt/s2l')
UNIT = 's2l-web-update.service'
TAG = re.compile(r'v[0-9]+\.[0-9]+\.[0-9]+')
SHA = re.compile(r'[a-f0-9]{40}')
PHASES = {'preparing', 'building', 'backup', 'installing', 'restarting', 'verifying', 'rolling_back'}


def now():
    return datetime.now(timezone.utc).isoformat()


@contextlib.contextmanager
def lock(name, nonblocking=False):
    import fcntl
    ROOT.mkdir(parents=True, exist_ok=True, mode=0o700)
    if ROOT.is_symlink():
        raise RuntimeError('更新状态目录不能是符号链接')
    ROOT.chmod(0o700)
    with (ROOT / name).open('a') as stream:
        try:
            fcntl.flock(stream, fcntl.LOCK_EX | (fcntl.LOCK_NB if nonblocking else 0))
        except BlockingIOError as error:
            raise RuntimeError('更新任务正在处理中，请稍后重试') from error
        yield


def read(name):
    try:
        with (ROOT / name).open('rb') as source:
            data = source.read(16385)
    except FileNotFoundError:
        return {}
    if len(data) > 16384:
        raise RuntimeError('更新状态文件过大')
    result = json.loads(data)
    if not isinstance(result, dict):
        raise RuntimeError('更新状态格式无效')
    return result


def save(name, data):
    fd, path = tempfile.mkstemp(prefix='.update-', dir=ROOT)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8') as target:
            json.dump(data, target, ensure_ascii=True)
            target.flush()
            os.fsync(target.fileno())
        os.replace(path, ROOT / name)
        directory = os.open(ROOT, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(path)


def command(*args):
    result = subprocess.run(args, capture_output=True, text=True, timeout=10, check=False)
    if result.returncode:
        raise RuntimeError('系统更新服务操作失败')
    return result.stdout.strip()


def unit_active():
    result = subprocess.run(('systemctl', 'show', UNIT, '--property=ActiveState', '--property=LoadState'),
                            capture_output=True, text=True, timeout=5, check=False,
                            env={**os.environ, 'LC_ALL': 'C'})
    fields = dict(line.split('=', 1) for line in result.stdout.splitlines() if '=' in line)
    if fields.get('LoadState') == 'not-found':
        return False
    state = fields.get('ActiveState')
    if state not in ('active', 'activating', 'deactivating', 'reloading', 'inactive', 'failed'):
        raise RuntimeError('无法确认更新任务状态，请检查 systemd')
    return state not in ('inactive', 'failed')


def job_status():
    job = read('job.json')
    if job.get('status') in ('queued', 'running'):
        age = time.time() - job.get('started_epoch', 0)
        if not (job['status'] == 'queued' and 0 <= age < 30) and not unit_active():
            job.update(status='interrupted', finished_at=now(), message='更新任务已中断，请检查服务状态后重试')
            save('job.json', job)
    return job


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def github(path):
    request = Request('https://api.github.com/repos/lucaswren/s2l/' + path,
                      headers={'Accept': 'application/vnd.github+json', 'User-Agent': 'S2L-Updater'})
    opener = build_opener(ProxyHandler({}), NoRedirect())
    try:
        with opener.open(request, timeout=8) as response:
            data = response.read(1024 * 1024 + 1)
    except Exception as error:
        raise RuntimeError('无法检查 GitHub 更新，请检查服务器网络或稍后重试') from error
    if len(data) > 1024 * 1024:
        raise RuntimeError('更新信息响应过大')
    result = json.loads(data)
    if not isinstance(result, dict):
        raise RuntimeError('更新信息格式无效')
    return result


def check():
    if job_status().get('status') in ('queued', 'running'):
        raise RuntimeError('更新正在进行中')
    release = github('releases/latest')
    tag = release.get('tag_name', '')
    if release.get('draft') or release.get('prerelease') or not isinstance(tag, str) or len(tag) > 64 or not TAG.fullmatch(tag):
        raise RuntimeError('未找到可用的正式版本')
    commit = github('commits/' + tag).get('sha', '')
    if not isinstance(commit, str) or not SHA.fullmatch(commit):
        raise RuntimeError('更新提交信息无效')
    result = {'tag': tag, 'commit': commit, 'checked_at': now(), 'checked_epoch': time.time(),
              'url': 'https://github.com/lucaswren/s2l/releases/tag/' + tag}
    save('release.json', result)
    return result


def preflight():
    if not all(shutil.which(item) for item in ('systemctl', 'systemd-run', 'bash', 'python3', 'curl', 'tar')) or not (shutil.which('go') or os.access('/usr/local/go/bin/go', os.X_OK)):
        raise RuntimeError('缺少更新依赖，请检查 systemd、Go、curl 和 tar')
    if APP.is_symlink() or not (APP / 'remote_update.sh').is_file():
        raise RuntimeError('仅支持默认 /opt/s2l 安装目录')
    from verify_update import preflight as verify
    verify(APP / 'config.json')


def start(body):
    preflight()
    if unit_active() or job_status().get('status') in ('queued', 'running'):
        raise RuntimeError('已有更新任务正在运行')
    release = read('release.json')
    age = time.time() - release.get('checked_epoch', 0)
    if not (0 <= age <= 900) or not TAG.fullmatch(str(release.get('tag', ''))) or not SHA.fullmatch(str(release.get('commit', ''))):
        raise RuntimeError('更新信息已过期，请重新检查')
    if body.get('tag') != release['tag'] or body.get('commit') != release['commit']:
        raise RuntimeError('更新版本已变化，请重新检查')
    job = {'id': uuid.uuid4().hex, 'status': 'queued', 'phase': 'preparing', 'tag': release['tag'],
           'commit': release['commit'], 'started_at': now(), 'started_epoch': time.time()}
    save('job.json', job)
    try:
        command('systemd-run', '--quiet', '--collect', '--unit=' + UNIT,
                '--property=Type=exec', '--property=User=root', '--property=WorkingDirectory=' + str(APP), '--property=KillMode=control-group', '--property=UMask=0077', '--property=RuntimeMaxSec=1800',
                '--property=TimeoutStopSec=90', '--property=StandardOutput=null', '--property=StandardError=null',
                '/usr/bin/python3', str(APP / 'manage_update.py'), 'run', job['id'])
    except Exception:
        job.update(status='failed', finished_at=now(), message='无法启动后台更新任务')
        save('job.json', job)
        raise
    return job


def phase(job_id, value):
    if value not in PHASES:
        raise ValueError('invalid phase')
    job = read('job.json')
    if job.get('id') == job_id and job.get('status') == 'running':
        job['phase'] = value
        save('job.json', job)


FAILURE_MESSAGES = {
    'build_cache': '更新失败：Go 构建缓存不可用，请检查目录权限。',
    'toolchain': '更新失败：Go 工具链不可用，请检查版本和下载连接。',
    'disk_full': '更新失败：磁盘空间不足。',
    'network': '更新失败：源码或工具链下载失败，请检查服务器网络。',
    'build_failed': '更新失败：程序编译阶段未完成，请检查构建环境。',
    'update_failed': '更新失败，请核对服务状态和升级备份',
}


def execute_update(env):
    # Drain output without retaining credentials, compiler paths or unbounded
    # logs. Only a fixed diagnostic code is persisted with the job result.
    tail = bytearray()
    with subprocess.Popen(('bash', str(APP / 'remote_update.sh')), env=env,
                          stdout=subprocess.PIPE, stderr=subprocess.STDOUT) as process:
        while True:
            chunk = process.stdout.read(4096)
            if not chunk:
                break
            tail.extend(chunk)
            del tail[:-16384]
        code = process.wait()
    text = tail.decode('utf-8', errors='replace').lower()
    reason = 'update_failed'
    for name, markers in (
        ('disk_full', ('no space left on device', 'disk quota exceeded')),
        ('build_cache', ('build cache is required', 'failed to initialize build cache', 'gocache is not defined')),
        ('toolchain', ('requires go >=', 'go.mod requires go', 'toolchain not available', 'cannot find goroot')),
        ('network', ('could not resolve host', 'failed to connect', 'connection timed out', 'tls handshake timeout', 'curl: (')),
    ):
        if any(marker in text for marker in markers):
            reason = name
            break
    return code, reason


def run(job_id):
    # Independent systemd unit survives stopping s2l.service.
    with lock('worker.lock', nonblocking=True):
        with lock('control.lock'):
            job = read('job.json')
            if job.get('id') != job_id or job.get('status') != 'queued':
                raise RuntimeError('更新任务已失效')
            if not SHA.fullmatch(str(job.get('commit', ''))) or not TAG.fullmatch(str(job.get('tag', ''))):
                raise RuntimeError('更新任务版本无效')
            job.update(status='running', phase='preparing')
            save('job.json', job)
        code, reason = -1, 'update_failed'
        try:
            env = {**os.environ, 'S2L_REF': job['commit'], 'S2L_COMMIT': job['commit'],
                   'S2L_VERSION': job['tag'], 'S2L_WEB_UPDATE_ID': job_id}
            code, reason = execute_update(env)
        finally:
            with lock('control.lock'):
                latest = read('job.json')
                if latest.get('id') == job_id:
                    if code != 0 and reason == 'update_failed' and latest.get('phase') == 'building':
                        reason = 'build_failed'
                    latest.update(status='succeeded' if code == 0 else 'failed', exit_code=code, finished_at=now(),
                                  message='更新完成，请刷新页面' if code == 0 else FAILURE_MESSAGES[reason])
                    if code != 0:
                        latest['error_code'] = reason
                    save('job.json', latest)
        if code != 0:
            raise RuntimeError('后台更新未完成，请检查服务与备份')


def main():
    if os.geteuid() != 0:
        raise RuntimeError('网页更新需要 root 权限')
    action = sys.argv[1] if len(sys.argv) > 1 else ''
    if action == 'run' and len(sys.argv) == 3:
        run(sys.argv[2])
        return
    with lock('control.lock', nonblocking=True):
        if action == 'status':
            result = {'job': job_status(), 'release': read('release.json')}
        elif action == 'check':
            preflight()
            result = {'release': check(), 'job': job_status()}
        elif action == 'start':
            data = sys.stdin.read(4097)
            body = json.loads(data) if len(data) <= 4096 else None
            if not isinstance(body, dict):
                raise ValueError('invalid request')
            # Probe the previous worker while holding control.lock, then release
            # worker.lock BEFORE systemd starts the new worker. Otherwise its
            # nonblocking acquisition races this request and can fail instantly.
            with lock('worker.lock', nonblocking=True):
                pass
            result = {'job': start(body), 'release': read('release.json')}
        elif action == 'phase' and len(sys.argv) == 4:
            phase(sys.argv[2], sys.argv[3])
            result = {}
        else:
            raise ValueError('invalid action')
    print(json.dumps(result, ensure_ascii=True))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(json.dumps({'error': str(error)}, ensure_ascii=True))
        sys.exit(1)

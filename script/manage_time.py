#!/usr/bin/env python3
"""Manage system time settings; no request data or diagnostic logs are retained."""
import contextlib
import ipaddress
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

CONFIG = Path('/etc/systemd/timesyncd.conf.d/90-s2l.conf')
HEADER = '# Managed by S2L time settings\n'
OTHER_UNITS = ('chrony.service', 'chronyd.service', 'ntp.service', 'ntpd.service', 'ntpsec.service', 'openntpd.service')


def run(*args):
    result = subprocess.run(args, capture_output=True, text=True, timeout=3, check=False,
                            env={**os.environ, 'LC_ALL': 'C'})
    if result.returncode:
        raise RuntimeError('命令失败：' + ' '.join(args[:2]))
    return result.stdout.strip()


def loaded(unit):
    result = subprocess.run(('systemctl', 'show', unit, '--property=LoadState', '--value'),
                            capture_output=True, text=True, timeout=3,
                            env={**os.environ, 'LC_ALL': 'C'})
    state = result.stdout.strip()
    if state == 'not-found':
        return False
    if result.returncode:
        raise RuntimeError('无法检查时间服务状态')
    return state == 'loaded'


def zones():
    return sorted(set(run('timedatectl', 'list-timezones').splitlines() + ['UTC']))


def custom_available():
    try:
        return loaded('systemd-timesyncd.service') and not any(loaded(unit) for unit in OTHER_UNITS)
    except (RuntimeError, subprocess.TimeoutExpired):
        return False


def read_config():
    if CONFIG.is_symlink():
        raise RuntimeError('S2L 时间配置不能是符号链接')
    try:
        with CONFIG.open('rb') as source:
            data = source.read(8193)
    except FileNotFoundError:
        return None
    if len(data) > 8192 or not data.startswith(HEADER.encode()):
        raise RuntimeError('时间配置文件已由其他程序管理，请先检查冲突')
    return data


def status(include_zones=True):
    fields = dict(line.split('=', 1) for line in run('timedatectl', 'show',
                  '--property=Timezone', '--property=NTP', '--property=NTPSynchronized', '--property=CanNTP').splitlines() if '=' in line)
    required = {'Timezone', 'NTP', 'NTPSynchronized', 'CanNTP'}
    if not required.issubset(fields) or not fields['Timezone'] or any(fields[key] not in ('yes', 'no') for key in required - {'Timezone'}):
        raise RuntimeError('系统时间状态不完整，未修改配置')
    data = read_config()
    servers = []
    if data:
        for line in data.decode().splitlines():
            if line.startswith('NTP='):
                servers = line[4:].split()
    current = ''
    try:
        current = run('timedatectl', 'show-timesync', '--property=ServerName', '--value')
    except (RuntimeError, subprocess.TimeoutExpired):
        pass
    return {'timezone': fields.get('Timezone', 'UTC'), 'auto_sync': fields.get('NTP') == 'yes',
            'synchronized': fields.get('NTPSynchronized') == 'yes', 'can_ntp': fields.get('CanNTP') == 'yes',
            'mode': 'custom' if servers else 'system', 'servers': servers, 'active_server': current,
            'custom_available': custom_available(), 'timezones': zones() if include_zones else []}


def validate(body, available_zones):
    if not isinstance(body, dict) or body.get('timezone') not in available_zones:
        raise ValueError('请选择有效的 IANA 时区')
    if type(body.get('auto_sync')) is not bool or body.get('mode') not in ('system', 'custom'):
        raise ValueError('自动同步开关或 NTP 模式无效')
    servers = body.get('servers', [])
    if not isinstance(servers, list) or len(servers) > 8:
        raise ValueError('最多设置 8 个 NTP 服务器')
    if body['mode'] == 'custom' and not servers:
        raise ValueError('请输入自定义 NTP 域名或 IP')
    for host in servers:
        if not isinstance(host, str) or len(host) > 253 or '%' in host or any(c.isspace() or ord(c) < 32 or ord(c) == 127 for c in host):
            raise ValueError('NTP 地址无效')
        try:
            ipaddress.ip_address(host)
            continue
        except ValueError:
            pass
        if not re.fullmatch(r'[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?', host) or any(
                not re.fullmatch(r'[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?', label) for label in host.split('.')):
            raise ValueError('NTP 仅支持域名或 IP，不支持 URL、端口或参数')


def publish(data):
    CONFIG.parent.mkdir(parents=True, exist_ok=True)
    if data is None:
        CONFIG.unlink(missing_ok=True)
    else:
        fd, name = tempfile.mkstemp(prefix='.s2l-time-', dir=CONFIG.parent)
        try:
            with os.fdopen(fd, 'wb') as f:
                os.fchmod(f.fileno(), 0o644)
                f.write(data)
                f.flush()
                os.fsync(f.fileno())
            os.replace(name, CONFIG)
        finally:
            with contextlib.suppress(FileNotFoundError):
                os.unlink(name)
    fd = os.open(CONFIG.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def apply(body):
    validate(body, zones())
    previous = status(False)
    original = read_config()
    if body['mode'] == 'custom' and not previous['custom_available']:
        raise ValueError('自定义 NTP 需要 systemd-timesyncd 且没有其他 NTP 服务；请先检查现有 chrony/ntpd 配置')
    desired = None if body['mode'] == 'system' else (HEADER + '[Time]\nNTP=' + ' '.join(body['servers']) + '\nFallbackNTP=\n').encode()
    try:
        if desired != original:
            publish(desired)
        run('timedatectl', 'set-timezone', body['timezone'])
        run('timedatectl', 'set-ntp', 'true' if body['auto_sync'] else 'false')
        if body['auto_sync'] and (body['mode'] == 'custom' or desired != original) and previous['custom_available']:
            run('systemctl', 'restart', 'systemd-timesyncd.service')
    except Exception as error:
        rollback_errors = []
        actions = [lambda: publish(original),
                   lambda: run('timedatectl', 'set-timezone', previous['timezone']),
                   lambda: run('timedatectl', 'set-ntp', 'true' if previous['auto_sync'] else 'false')]
        if previous['auto_sync'] and previous['custom_available']:
            actions.append(lambda: run('systemctl', 'restart', 'systemd-timesyncd.service'))
        for action in actions:
            try:
                action()
            except Exception:
                rollback_errors.append(True)
        raise RuntimeError('时间设置失败；' + ('回滚未完全成功，请检查系统时间服务' if rollback_errors else '已恢复原配置')) from error
    # NTP sync is asynchronous; a successful save does not imply synchronization.
    return {'message': '时间配置已保存；自动同步由系统时间服务执行，请刷新查看同步状态'}


def main():
    import fcntl
    if os.geteuid() != 0:
        raise RuntimeError('系统时间配置需要 root 权限')
    with open('/run/lock/s2l-time.lock', 'a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise RuntimeError('时间服务正在处理中，请稍后重试') from error
        if len(sys.argv) != 2:
            raise ValueError('invalid action')
        if sys.argv[1] == 'status':
            result = status()
        elif sys.argv[1] == 'apply':
            data = sys.stdin.read(8193)
            if len(data) > 8192:
                raise ValueError('请求过大')
            result = apply(json.loads(data))
        else:
            raise ValueError('invalid action')
        print(json.dumps(result, ensure_ascii=False))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(json.dumps({'error': str(error)}, ensure_ascii=False))
        sys.exit(1)

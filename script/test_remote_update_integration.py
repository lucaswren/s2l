"""Opt-in systemd updater test using only private fixtures and loopback HTTP.
Run as root with S2L_UPDATE_SYSTEMD_TEST=1 in a disposable Linux test VM.
"""
import io
import json
import os
from pathlib import Path
import signal
import subprocess
import tarfile
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import uuid


def wait_result(path, predicate, timeout=45):
    deadline = time.monotonic()+timeout
    while time.monotonic()<deadline:
        if path.exists():
            try:
                result=json.loads(path.read_text())
                if predicate(result): return result
            except json.JSONDecodeError: pass
        time.sleep(.1)
    raise AssertionError('updater result timeout: '+str(path))


def main():
    if os.geteuid()!=0 or os.environ.get('S2L_UPDATE_SYSTEMD_TEST')!='1':
        raise SystemExit('requires explicit opt-in in a disposable Linux VM')
    source=(Path(__file__).resolve().parent/'remote_update.sh').read_text()
    sha='a'*40
    with tempfile.TemporaryDirectory(prefix='s2l-detached-update-') as directory:
        root=Path(directory);app=root/'app';app.mkdir()
        (app/'config.json').write_text('{}')
        (app/'s2l').write_text('#!/bin/sh\nexit 0\n');(app/'s2l').chmod(0o700)
        archive=b'';requests=[]
        class Handler(BaseHTTPRequestHandler):
            def log_message(self,*args): pass
            def do_GET(self):
                requests.append(self.path)
                if self.path.endswith('/releases/latest'):data=json.dumps(dict(tag_name='1.0.2')).encode()
                elif '/commits/' in self.path:data=json.dumps(dict(sha=sha)).encode()
                else:data=archive
                self.send_response(200);self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
        server=ThreadingHTTPServer(('127.0.0.1',0),Handler)
        threading.Thread(target=server.serve_forever,daemon=True).start()
        try:
            for exit_code in (0,9):
                job=root/('job'+str(exit_code));permit=root/('permit'+str(exit_code));marker=root/('marker'+str(exit_code))
                unit='s2l-update-fixture-'+uuid.uuid4().hex+'.service'
                payload=('#!/bin/bash\necho fixture-building\nwhile [[ ! -f %s ]]; do sleep .1; done\nprintf \'%%s|%%s\' "$S2L_VERSION" "$S2L_COMMIT" > %s\nexit %s\n' % (permit,marker,exit_code)).encode()
                data=io.BytesIO()
                with tarfile.open(fileobj=data,mode='w:gz') as tar:
                    item=tarfile.TarInfo('s2l/script/update.sh');item.size=len(payload);tar.addfile(item,io.BytesIO(payload))
                archive=data.getvalue()
                # Only the fixture copy uses loopback and disposable paths.
                script=source.replace('/opt/s2l',str(app)).replace('/var/lib/s2l/cli-update',str(job)).replace('/run/s2l-cli-update-launch.lock',str(root/'launch.lock')).replace('s2l-cli-update.service',unit)
                base='http://127.0.0.1:'+str(server.server_port)
                script=script.replace('https://api.github.com/repos/lucaswren/s2l',base+'/api').replace('https://codeload.github.com/lucaswren/s2l',base+'/source')
                env=dict(os.environ)
                for key in ('S2L_CLI_UPDATE','S2L_WEB_UPDATE_ID','S2L_REF','S2L_COMMIT','S2L_VERSION'):env.pop(key,None)
                # Hang up the process group while the SSH follower is attached.
                process=subprocess.Popen(['bash'],stdin=subprocess.PIPE,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,start_new_session=True,env=env)
                try:
                    process.stdin.write(script.encode());process.stdin.close()
                    wait_result(job/'result.json',lambda r:r.get('status')=='running' and r.get('commit')==sha)
                    deadline=time.monotonic()+20
                    while 'fixture-building' not in (job/'update.log').read_text() and time.monotonic()<deadline:
                        time.sleep(.1)
                    assert 'fixture-building' in (job/'update.log').read_text(), 'worker never reached the build barrier'
                    os.killpg(process.pid,signal.SIGHUP);process.wait(timeout=10)
                    assert not marker.exists(), 'worker finished before the hangup injection'
                    permit.touch()
                    resumed=subprocess.run(['bash','-s','--','--follow'],input=script,text=True,env=env,
                                           capture_output=True,timeout=30)
                    assert resumed.returncode==exit_code,resumed
                    assert 'fixture-building' in resumed.stdout and ('Upgrade succeeded' if exit_code==0 else 'Upgrade failed') in resumed.stdout,resumed
                    result=wait_result(job/'result.json',lambda r:r.get('status') in ('succeeded','failed'))
                    assert result['exit_code']==exit_code,result
                    assert result['status']==('succeeded' if exit_code==0 else 'failed'),result
                    assert result['version']=='1.0.2' and result['commit']==sha,result
                    assert marker.read_text()=='1.0.2|'+sha
                    assert (app/'s2l').read_text()=='#!/bin/sh\nexit 0\n'
                    assert (job/'result.json').stat().st_mode & 0o077==0
                    print('PASS: real systemd worker survived SSH-style SIGHUP; exit='+str(exit_code),flush=True)
                finally:
                    if process.poll() is None:
                        os.killpg(process.pid,signal.SIGKILL);process.wait(timeout=10)
                    subprocess.run(['systemctl','stop',unit],capture_output=True)
                    subprocess.run(['systemctl','reset-failed',unit],capture_output=True)
            assert any('/source/tar.gz/'+sha in path for path in requests)
            assert not any('/tar.gz/main' in path for path in requests)
        finally:
            server.shutdown();server.server_close()


if __name__=='__main__':main()

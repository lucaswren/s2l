import {mkdir, mkdtemp, rm} from 'node:fs/promises';
import {createServer} from 'node:net';
import {basename, dirname, join, resolve} from 'node:path';

export const pause = ms => new Promise(resolve => setTimeout(resolve, ms));

export function freePort() {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.on('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port;
      server.close(error => error ? reject(error) : resolve(port));
    });
  });
}

export async function createFixtureDir(cwd, prefix) {
  if (!['ui-chrome-', 'auth-ui-'].includes(prefix)) throw Error('Unknown UI fixture prefix');
  const root = resolve(cwd, 'tmp');
  await mkdir(root, {recursive: true});
  return mkdtemp(join(root, prefix));
}

async function stopChild(child) {
  if (!child?.pid || child.exitCode !== null || child.signalCode !== null) return;
  await new Promise((resolve, reject) => {
    const force = setTimeout(() => child.kill('SIGKILL'), 5000);
    const timeout = setTimeout(() => {
      clearTimeout(force);
      reject(Error(`UI fixture process ${child.pid} did not stop`));
    }, 10000);
    child.once('close', () => {
      clearTimeout(force);
      clearTimeout(timeout);
      resolve();
    });
    child.kill();
  });
}

export async function disposeFixture(cwd, dir, children) {
  const path = resolve(dir);
  if (dirname(path) !== resolve(cwd, 'tmp') || !/^(ui-chrome-|auth-ui-)[A-Za-z0-9]+$/.test(basename(path))) {
    throw Error('Refusing to remove a directory outside the UI fixtures');
  }
  const results = await Promise.allSettled(children.map(stopChild));
  const failures = results.filter(result => result.status === 'rejected');
  if (failures.length) throw new AggregateError(failures.map(result => result.reason), 'UI fixture cleanup failed');
  await rm(path, {recursive: true, force: true, maxRetries: 20, retryDelay: 100});
}

import test from 'node:test';
import assert from 'node:assert/strict';
import {api} from '../src/api/client.js';

test('malformed successful responses cannot report a successful save', async t => {
 for (const body of ['<html>upstream error</html>', '', 'null', 'true', '"saved"', '{"ok":']) {
  t.mock.method(globalThis,'fetch',async()=>new Response(body,{status:200}));
  await assert.rejects(api.saveNode({name:'n'}), /无效数据/);
  t.mock.restoreAll();
 }
});
test('arrays and JSON objects remain supported', async t => {
 t.mock.method(globalThis,'fetch',async()=>Response.json([]));
 assert.deepEqual(await api.listNodes(),[]);
 t.mock.restoreAll();
 t.mock.method(globalThis,'fetch',async()=>Response.json({enabled:false}));
 assert.deepEqual(await api.debug(),{enabled:false});
});
test('body cancellation remains an abort, not an empty successful result', async t => {
 t.mock.method(globalThis,'fetch',async()=>({ok:true,json:async()=>{throw new DOMException('cancelled','AbortError');}}));
 await assert.rejects(api.debug(), {name:'AbortError'});
});
test('non-JSON failure responses retain HTTP status', async t => {
 t.mock.method(globalThis,'fetch',async()=>new Response('<html>busy</html>',{status:503,statusText:'Service Unavailable'}));
 await assert.rejects(api.settings(),/Service Unavailable/);
});

test('log export rejects HTML and invalid ZIP bodies before download', async t => {
 for (const [body,type] of [['<html>login</html>','text/html'], ['truncated response','application/zip']]) {
  t.mock.method(globalThis,'fetch',async()=>new Response(body,{status:200,headers:{'Content-Type':type}}));
  await assert.rejects(api.exportDebug(),/ZIP/);
  t.mock.restoreAll();
 }
});

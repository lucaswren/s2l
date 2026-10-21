import test from 'node:test';
import assert from 'node:assert/strict';
import { setLanguage } from '../src/i18n/index.js';

// Assertions must not depend on the host operating system language.
test.beforeEach(() => setLanguage('zh-CN'));
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

test('resource revisions travel in If-Match and never enter request JSON', async t => {
 let saved;
 t.mock.method(globalThis,'fetch',async(path,options)=>{if(options.method==='POST'){saved=options;return Response.json({ok:true})}return new Response('{"id":"n"}',{headers:{'Content-Type':'application/json','ETag':'"revision-one"'}})});
 const row=await api.getNode('n');assert.equal(row._revision,'"revision-one"');assert.equal(JSON.stringify(row),'{"id":"n"}');await api.saveNode({...row},row._revision);assert.equal(saved.headers['If-Match'],'"revision-one"');assert.equal(saved.body,'{"id":"n"}');
});

test('cookie requests never retain Basic credentials and API expiry triggers sign-in',async t=>{
 const previous=globalThis.window,events=[];globalThis.window={dispatchEvent:event=>events.push(event.type)};
 t.after(()=>{if(previous===undefined)delete globalThis.window;else globalThis.window=previous});
 let options;
 t.mock.method(globalThis,'fetch',async(_,value)=>{options=value;return Response.json({error:'authentication required'},{status:401})});
 await assert.rejects(api.listNodes());assert.equal(options.credentials,'same-origin');assert.equal(options.headers.Authorization,undefined);assert.deepEqual(events,['s2l-auth-required']);
 events.length=0;await assert.rejects(api.login({username:'u',password:'p'}));assert.deepEqual(events,[]);
 await assert.rejects(api.exportDebug());assert.deepEqual(events,['s2l-auth-required']);
});

test('late 401s from a previous session cannot sign out a new login',async t=>{
 const previous=globalThis.window,events=[];globalThis.window={dispatchEvent:event=>events.push(event.type)};
 t.after(()=>{if(previous===undefined)delete globalThis.window;else globalThis.window=previous});
 const delayed=[];
 t.mock.method(globalThis,'fetch',async(path,options)=>{

  if(path==='/api/auth/login')return Response.json({authenticated:true});
  return new Promise(resolve=>delayed.push(()=>resolve(Response.json({error:'authentication required'},{status:401}))));
 });
 const requests=[api.listNodes(),api.exportDebug(),api.streamNetworkTest('n',{kind:'mtr'},undefined,()=>{})].map(p=>assert.rejects(p));
 await api.login({username:'admin',password:'fixture'});
 delayed.splice(0).forEach(resolve=>resolve());await Promise.all(requests);
 assert.deepEqual(events,[]);
 const current=assert.rejects(api.listNodes());delayed.shift()();await current;
 assert.deepEqual(events,['s2l-auth-required']);
});

test('malformed authentication success cannot enter the management UI',async t=>{
 for(const value of [{},{authenticated:false},[],{authenticated:'true'}]){
  t.mock.method(globalThis,'fetch',async()=>Response.json(value));
  await assert.rejects(api.login({username:'u',password:'p'}));t.mock.restoreAll();
 }
});

test('speed tests require independent download and upload results',async t=>{
 for (const result of [{ok:true,speed_mbps:10},{ok:false,download:{ok:false}}]) {
  t.mock.method(globalThis,'fetch',async()=>Response.json(result));
  await assert.rejects(api.speedTestNode('n'),/无效数据/);t.mock.restoreAll();
 }
 const result={ok:true,download:{ok:true,speed_mbps:10},upload:{ok:true,speed_mbps:5}};
 t.mock.method(globalThis,'fetch',async()=>Response.json(result));
 assert.deepEqual(await api.speedTestNode('n'),result);
});

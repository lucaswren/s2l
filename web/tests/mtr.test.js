import test from 'node:test';
import assert from 'node:assert/strict';
import {mtrHops} from '../src/utils/mtr.js';
import {api} from '../src/api/client.js';

test('raw MTR handles sends, replies, duplicate sequences and missing hops', () => {
 const hops=mtrHops('x 0 7\nh 0 192.0.2.1\np 0 1000 7\np 0 1000 7\nx 2 8\nh 2 2001:db8::1\np 2 2000 8\np 2 4000 9\n');
 assert.equal(hops.length,3);assert.equal(hops[0].sent,1);assert.equal(hops[0].received,1);assert.equal(hops[0].average,'1.00');assert.equal(hops[1].address,'—');assert.equal(hops[2].average,'3.00');
 assert.deepEqual(mtrHops('h 999999 bad\np -1 3 1\npermission denied\n'),[]);
});
test('raw replies require a sequence identifier', () => {
 const [hop]=mtrHops('h 0 192.0.2.1\np 0 1500\np 0 2500\n');
 assert.equal(hop.sent,0);assert.equal(hop.received,0);assert.equal(hop.average,'—');
});
test('MTR streams chunked UTF-8 data before completion', async t => {
 const encoder=new TextEncoder(), events=[];
 const payload=encoder.encode('{"type":"start","data":{"kind":"mtr","target":"节点"}}\n{"type":"output","data":"h 0 192.0.2.1\\n"}\n{"type":"result","data":{"ok":true,"kind":"mtr","output":""}}\n');
 t.mock.method(globalThis,'fetch',async () => new Response(new ReadableStream({start(controller){for(let i=0;i<payload.length;i+=2)controller.enqueue(payload.slice(i,i+2));controller.close();}}),{headers:{'Content-Type':'application/x-ndjson'}}));
 const result=await api.streamNetworkTest('n',{kind:'mtr'},undefined,event => events.push(event));
 assert.equal(events[0].data.target,'节点');assert.equal(events[1].data,'h 0 192.0.2.1\n');assert.equal(result.ok,true);
});
test('MTR rejects truncated or non-stream responses', async t => {
 for(const [body,type,pattern] of [['{"type":"start","data":{"kind":"mtr"}}\n','application/x-ndjson',/中断/],['<html>error</html>','text/html',/实时追踪/],['{"type":"wrong","data":{}}\n','application/x-ndjson',/格式无效/]]) {
  t.mock.method(globalThis,'fetch',async () => new Response(body,{headers:{'Content-Type':type}}));
  await assert.rejects(api.streamNetworkTest('n',{kind:'mtr'}),pattern);t.mock.restoreAll();
 }
});

test('MTR rejects events out of order and malformed completion', async t => {
 const start={type:'start',data:{kind:'mtr'}};
 for(const events of [
  [{type:'output',data:'h 0 192.0.2.1\n'}],
  [start,start],
  [start,{type:'result',data:{ok:true}}],
  [start,{type:'result',data:{kind:'mtr',ok:true,output:''}},{type:'output',data:'late'}],
 ]) {
  t.mock.method(globalThis,'fetch',async () => new Response(events.map(x => JSON.stringify(x)).join('\n')+'\n',{headers:{'Content-Type':'application/x-ndjson'}}));
  await assert.rejects(api.streamNetworkTest('n',{kind:'mtr'}),/顺序无效|结果无效/);t.mock.restoreAll();
 }
});

test('partial MTR events wait for a newline before updating statistics', () => {
 assert.equal(mtrHops('x 0 1\np 0 12')[0].received,0);
 assert.equal(mtrHops('x 0 1\np 0 12345 1\n')[0].last,'12.35');
});

test('aborting an HTTP error body remains cancellation', async t => {
 t.mock.method(globalThis,'fetch',async () => ({ok:false,json:async () => {throw new DOMException('cancelled','AbortError');}}));
 await assert.rejects(api.streamNetworkTest('n',{kind:'mtr'}),{name:'AbortError'});
});

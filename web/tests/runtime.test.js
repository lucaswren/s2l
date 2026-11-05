import test from 'node:test';
import assert from 'node:assert/strict';
import {normalizeStatus,sampleRuntime,validSampleTime,mbps} from '../src/utils/runtime.js';
const at=seconds=>new Date(Date.UTC(2026,10,6)+seconds*1000).toISOString();
function snapshot(second,bytes=0,errors=82){return normalizeStatus({collected_at:at(second),uptime_sec:100+second,sessions_checked_at:at(second),
 sessions:[{index:4,interface:'ppp0',mapping_id:'m',peer_ip:'10.0.1.2',connected_at:at(-100),rx_bytes:bytes,tx_bytes:bytes*2}],
 singbox_pids:{tun101:123},upstream_health:{m:{tun_name:'tun101',process:{started_at:at(-100),error_lines:errors}}},
 host_stats:{RetransSegs:100+second,OutSegs:10000+second*100}});}
test('status normalization preserves timestamps, host statistics and startup state',()=>{
 const value=normalizeStatus({collected_at:at(0),host_stats:{RetransSegs:42},startup_complete:true});
 assert.equal(value.collected_at,at(0));assert.equal(value.host_stats.RetransSegs,42);assert.equal(value.startup_complete,true);
 assert.deepEqual(normalizeStatus(null).sessions,[]);assert.equal(validSampleTime('0001-01-01T00:00:00Z'),null);
});
test('PPP rates use client direction and actual collection intervals',()=>{
 const value=sampleRuntime(snapshot(0),snapshot(4,500000));
 assert.equal(value.sessions[4].upload,1);assert.equal(value.sessions[4].download,2);
 assert.equal(value.processes.m,0);assert.equal(value.host.retrans,4);
 assert.equal(mbps(value.sessions[4].upload),'1.00 Mbps');assert.equal(mbps(null),'—');assert.equal(mbps(0),'0.00 Mbps');
});
test('only new errors are counted, not historical totals',()=>{
 assert.equal(sampleRuntime(snapshot(0),snapshot(5,0,85)).processes.m,3);
 for(const change of [s=>s.singbox_pids.tun101=124,s=>s.upstream_health.m.process.started_at=at(1),s=>s.upstream_health.m.process.error_lines=1,s=>s.uptime_sec=1]){
  const current=snapshot(5);change(current);assert.equal(sampleRuntime(snapshot(0),current).processes.m,null);
 }
});
test('PPP collector timestamps take precedence over the enclosing status timestamp',()=>{
 const old=snapshot(0),current=snapshot(5,500000);current.sessions_checked_at=at(4);
 assert.equal(sampleRuntime(old,current).sessions[4].upload,1);
 current.sessions_checked_at=at(-1);assert.equal(sampleRuntime(old,current).sessions[4].upload,null);
});
test('duplicate, missing, interrupted and reverse samples do not invent zero rates',()=>{
 for(const previous of [null,snapshot(5),snapshot(10),snapshot(-60)]){
  const value=sampleRuntime(previous,snapshot(5));assert.equal(value.sessions[4].upload,null);assert.equal(value.processes.m,null);
 }
});
test('reused PPP identity, retained sessions and counters that reset never create speed spikes',()=>{
 const old=snapshot(0,500000);
 for(const change of [s=>s.sessions[0].index=5,s=>s.sessions[0].connected_at=at(1),s=>s.sessions[0].peer_ip='10.0.1.3',s=>s.sessions_error='collection failed',s=>s.sessions_checked_at=old.sessions_checked_at,s=>s.sessions[0].rx_bytes=1]){
  const current=snapshot(5,600000);change(current);
  assert.equal(Object.values(sampleRuntime(old,current).sessions)[0].upload,null);
 }
 const retained=snapshot(0,500000);retained.sessions_error='retained';
 assert.equal(sampleRuntime(retained,snapshot(5,600000)).sessions[4].upload,null);
});
test('status fetches carry the refresh abort signal',async t=>{
 const {api}=await import('../src/api/client.js');const signal=new AbortController().signal;const calls=[];
 t.mock.method(globalThis,'fetch',async(path,options)=>{calls.push(options);return Response.json({})});
 await Promise.all([api.status(signal),api.listNodes(signal),api.listMappings(signal)]);
 assert.equal(calls.length,3);assert.ok(calls.every(options=>options.signal===signal));
});

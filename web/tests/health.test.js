import test from 'node:test';
import assert from 'node:assert/strict';
import { setLanguage } from '../src/i18n/index.js';

// Assertions must not depend on the host operating system language.
test.beforeEach(() => setLanguage('zh-CN'));
import {healthLabels,protocolSummary,passRate,isResolved} from '../src/utils/health.js';
import {api} from '../src/api/client.js';

test('restrictions retain stage, HTTP status and duration without implying node death',()=>{
 assert.match(healthLabels.restricted,/已响应/);
 assert.equal(protocolSummary({stage:'http',status_code:429,duration_ms:23}),'HTTP 响应 · HTTP 429 · 23 ms');
 assert.equal(protocolSummary(null),'');
 assert.equal(passRate({pass:3,fail:1,restricted:1}),'60.0%');
 assert.equal(passRate({}),'—');
});
test('history and mapping diagnostics pass cancellation and encode identifiers',async t=>{
 const calls=[],controller=new AbortController();
 t.mock.method(globalThis,'fetch',async(path,options)=>{calls.push({path,options});return new Response('{}',{headers:{'Content-Type':'application/json'}})});
 await api.history('a/b',controller.signal);await api.mappingDiagnostic('a/b',controller.signal);
 assert.equal(calls[0].path,'/api/history?mapping_id=a%2Fb');assert.equal(calls[1].path,'/api/mappings/a%2Fb/network-test');
 for(const c of calls)assert.equal(c.options.signal,controller.signal);
 assert.equal(calls[1].options.body,'{}');
});

test('Go zero timestamps are not resolved incidents',()=>{assert.equal(isResolved({resolved:'0001-01-01T00:00:00Z'}),false);assert.equal(isResolved({}),false);assert.equal(isResolved({resolved:'not-a-date'}),false);assert.equal(isResolved({resolved:'2026-10-20T12:00:00Z'}),true)});

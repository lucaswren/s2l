import test from 'node:test';
import assert from 'node:assert/strict';
import { setLanguage } from '../src/i18n/index.js';

// Assertions must not depend on the host operating system language.
test.beforeEach(() => setLanguage('zh-CN'));
import {compareGoogle,readGoogleSnapshot,rejectEvidence} from '../src/utils/google-diagnostic.js';
const hosts=['www.google.com','accounts.google.com','oauth2.googleapis.com','gmail.googleapis.com','www.youtube.com','gemini.google.com','robinfrontend-pa.googleapis.com','oauthaccountmanager.googleapis.com'];
const dns=()=>({state:'healthy',addresses:['203.0.113.1'],duration_ms:1});
const report=()=>({node_id:'n',node_name:'线路',checked_at:'2026-10-21T00:00:00Z',targets:hosts.map(host=>({host,a:dns(),aaaa:{state:'no_address',addresses:[]},doh:dns(),tcp_dns:dns(),tcp:'healthy',tls:'healthy',http:'responded',status:403,alpn:'h2',stage:'http'}))});
test('Google comparison distinguishes TLS failure, DNS address changes and HTTP status without IP-ban conclusions',()=>{
 const a=report(),b=report();b.targets[0].tls='unavailable';b.targets[1].a.addresses=['203.0.113.2'];b.targets[2].status=401;
 const rows=compareGoogle(a,b);assert.deepEqual(rows[0].fields,['tls']);assert.equal(rows[1].different,false);assert.equal(rows[1].dnsDifferent,true);assert.deepEqual(rows[2].fields,['status']);
 assert.equal(compareGoogle(a,null).length,0);
});
test('historical Google snapshots require matching bounded schema and never retain command or account data',()=>{
 const r=report();r.password='secret';r.command='ignored';const raw=JSON.stringify({schema_version:1,kind:'google',google:[r]});
 assert.throws(()=>readGoogleSnapshot(JSON.stringify({schema_version:1,kind:'google',google:[{...r,targets:r.targets.slice(0,6)}]})));
 const result=readGoogleSnapshot(raw);assert.equal(result.snapshot,true);assert.equal(result.password,undefined);assert.equal(result.command,undefined);assert.equal(result.targets[0].status,403);
 for(const v of [raw.replace('"schema_version":1','"schema_version":2'),raw.replace('accounts.google.com','evil.example'),raw.replace('"tls":"healthy"','"tls":"forged"'),'x'.repeat(512*1024+1)])assert.throws(()=>readGoogleSnapshot(v));
});
test('missing or reset reject counters never imply zero rejections',()=>{
 assert.match(rejectEvidence({}),/未取得/);
 const a={state:'healthy',udp:12,connections:7,udp_found:true,connections_found:true};
 assert.match(rejectEvidence({reject_before:a,reject_after:{...a,connections:9}}),/连接限额拒绝：增加 2/);
 assert.match(rejectEvidence({reject_before:a,reject_after:{...a,udp:0}}),/计数已重置/);
 assert.match(rejectEvidence({reject_before:a,reject_after:{...a,udp_found:false}}),/规则未找到/);
});

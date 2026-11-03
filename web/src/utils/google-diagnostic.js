import { tr } from '../i18n/index.js';
const states=new Set(['healthy','unavailable','not_tested','cancelled','no_address','nxdomain','responded']);
const string=(v,max=253)=>typeof v==='string'&&v.length<=max;
export function readGoogleSnapshot(raw){
 if(typeof raw!=='string'||raw.length>512*1024)throw Error(tr("对照文件超过 512 KiB"));
 const data=JSON.parse(raw);
 if(data.kind!=='google'||data.schema_version!==1||!Array.isArray(data.google)||!data.google.length)throw Error(tr("请选择 Google 专项检测导出文件"));
 const r=data.google[0];
 if(!string(r.node_id,128)||!string(r.node_name,128)||!Number.isFinite(Date.parse(r.checked_at))||!Array.isArray(r.targets)||r.targets.length!==8)throw Error(tr("对照结果格式无效"));
 const hosts=new Set();
 for(const t of r.targets){
  if(!string(t.host)||hosts.has(t.host)||!['www.google.com','accounts.google.com','oauth2.googleapis.com','gmail.googleapis.com','www.youtube.com','gemini.google.com','robinfrontend-pa.googleapis.com','oauthaccountmanager.googleapis.com'].includes(t.host))throw Error(tr("对照检测目标不匹配"));
  hosts.add(t.host);
  for(const k of ['a','aaaa','doh','tcp_dns'])if(!t[k]||!states.has(t[k].state)||!Array.isArray(t[k].addresses)||t[k].addresses.length>4096||t[k].addresses.some(a=>!string(a,64)))throw Error(tr("对照 DNS 结果无效"));
  if(t.https && (!states.has(t.https.state)||!Number.isInteger(t.https.records)||t.https.records<0||t.https.records>65535))throw Error(tr("对照 DNS 结果无效"));
  for(const k of ['tcp','tls','http'])if(!states.has(t[k]))throw Error(tr("对照阶段结果无效"));
  if(!Number.isInteger(t.status)||t.status<0||t.status>599||!string(t.alpn,32)||!string(t.stage,64))throw Error(tr("对照响应格式无效"));
 }
 // Keep only fields rendered by the comparison. Imported data never triggers a request.
 return {node_id:r.node_id,node_name:r.node_name,checked_at:r.checked_at,targets:r.targets.map(t=>({host:t.host,a:t.a,aaaa:t.aaaa,doh:t.doh,tcp_dns:t.tcp_dns,https:t.https?{state:t.https.state,records:t.https.records}:undefined,tcp:t.tcp,tls:t.tls,http:t.http,status:t.status,alpn:t.alpn,stage:t.stage})),snapshot:true};
}
export function compareGoogle(a,b){
 if(!a||!b)return [];
 return a.targets.map(x=>{const y=b.targets.find(t=>t.host===x.host);
  const fields=y?['tcp','tls','http','status','alpn'].filter(k=>x[k]!==y[k]):['missing'];
  if(y)for(const k of ['a','aaaa','doh','tcp_dns'])if(x[k]?.state!==y[k]?.state)fields.push(k);
  if(y && (x.https?.state!==y.https?.state || x.https?.records!==y.https?.records))fields.push('https');
  const dnsDifferent=!!y&&['a','aaaa'].some(k=>JSON.stringify([...(x[k]?.addresses||[])].sort())!==JSON.stringify([...(y[k]?.addresses||[])].sort()));
  return {host:x.host,a:x,b:y,different:fields.length>0,fields,dnsDifferent};
 });
}
export function rejectEvidence(m){
 const a=m.reject_before,b=m.reject_after;
 if(a?.state!=='healthy'||b?.state!=='healthy')return tr("拒绝计数未取得，不能按零命中处理");
 const count=(key,found)=>!a[found]||!b[found]?tr("规则未找到"):b[key]<a[key]?tr("计数已重置"):tr("增加 {0}", b[key]-a[key]);
 return tr("检测期间 UDP 拒绝：{0}；连接限额拒绝：{1}（不限额时无此规则）", count('udp','udp_found'), count('connections','connections_found'));
}

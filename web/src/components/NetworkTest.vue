<script setup>
import { computed, ref, watch, onBeforeUnmount } from 'vue';
import { api } from '../api/client';
import { mtrHops } from '../utils/mtr';
import {protocolSummary} from '../utils/health';
const props = defineProps({ node: Object });
const emit = defineEmits(['close', 'changed']);
const kind = ref('protocols'), mtrProtocol = ref('tcp');
const target = ref('www.example.com:443');
const stopped = ref(false);
const busy = ref(false), result = ref(null), error = ref(''), elapsed = ref(0);
const labels = {healthy:'正常', degraded:'仅备用可用', unavailable:'不可用', unsupported:'不支持',restricted:'目标已响应 · 状态异常'};
const methods = {https:'HTTPS 转发', 'dns-udp':'UDP 中继', 'udp-associate':'UDP 协商', doh:'DoH', tcp:'TCP DNS', 'tcp-fallback':'TCP DNS 备用', 'doh/tcp':'DoH / TCP DNS'};
const modes = [{value:'protocols',label:'协议检测'}, {value:'tcping',label:'TCPing'}, {value:'https',label:'HTTPS'}, {value:'quality',label:'传输质量'}, {value:'ping',label:'Ping'}, {value:'mtr',label:'MTR 追踪'}];
const mobileQuery = window.matchMedia('(max-width: 600px)');
const mobile = ref(mobileQuery.matches);
const resized = event => { mobile.value = event.matches; };
mobileQuery.addEventListener('change', resized);
const rows = computed(() => Object.entries(result.value?.protocols || {}).map(([name,value]) => ({name:name.toUpperCase(), ...value})));
const hops = computed(() => mtrHops(result.value?.output));
const summary = computed(() => stopped.value ? '测试已停止 · 保留部分结果' : busy.value ? `检测中 · ${elapsed.value} 秒` : result.value?.kind === 'mtr' && result.value.ok ? (result.value.reached ? '追踪完成 · 目标已响应' : '追踪完成 · 目标未响应') : result.value?.kind === 'https' && result.value?.protocols?.tcp?.state === 'restricted' ? '目标已响应 · 状态异常' : result.value?.ok ? (result.value.capability_warning ? 'TCP / DNS 正常 · UDP 能力受限' : '检测通过') : '检测完成 · 存在异常');
let controller, timer, disposed = false;
watch([kind,mtrProtocol], () => { if (!busy.value) { result.value=null;error.value='';stopped.value=false; } });
onBeforeUnmount(() => { disposed=true;controller?.abort();clearInterval(timer);mobileQuery.removeEventListener('change',resized); });
async function run() {
 if (busy.value) return;
 busy.value=true;stopped.value=false;error.value='';result.value=null;elapsed.value=0;
 controller=new AbortController();
 const started=Date.now();timer=setInterval(() => {elapsed.value=Math.floor((Date.now()-started)/1000);},1000);
 try {
  const body={kind:kind.value,target:target.value.trim(),mtr_protocol:mtrProtocol.value};
  if (kind.value === 'mtr') {
   result.value={kind:'mtr',output:''};
   result.value=await api.streamNetworkTest(props.node.id,body,controller.signal,event => {
    if (disposed) return;
    if (event.type === 'start') result.value={...event.data,output:''};
    if (event.type === 'output') result.value.output=(result.value.output+event.data).slice(0,32768);
   });
  } else { result.value=await api.networkTestNode(props.node.id,body,controller.signal); }
  if (!disposed) emit('changed');
 } catch(e) { if (!disposed) { stopped.value=e.name === 'AbortError';error.value=stopped.value ? '' : e.message;if(result.value) result.value={...result.value,partial:true,cancelled:stopped.value,finished_at:new Date().toISOString()}; } }
 finally {clearInterval(timer);if (!disposed) busy.value=false;}
}
function close() {controller?.abort();emit('close');}
function download() {
 const blob=new Blob([JSON.stringify(result.value,null,2)],{type:'application/json'});
 const url=URL.createObjectURL(blob), a=document.createElement('a');a.href=url;a.download='s2l-network-test.json';a.click();setTimeout(() => URL.revokeObjectURL(url),1000);
}
</script>
<template>
 <el-dialog :model-value="true" title="网络测试" class="network-test-dialog" width="760px" :fullscreen="mobile" :close-on-click-modal="false" :close-on-press-escape="!busy" append-to-body @close="close">
  <div class="network-test-node"><strong>{{ node.name }}</strong><span>{{ node.addr }}</span></div>
  <div class="network-test-modes" role="group" aria-label="测试类型"><el-button v-for="mode in modes" :key="mode.value" :type="kind === mode.value ? 'primary' : 'default'" :disabled="busy" :aria-pressed="kind === mode.value" @click="kind=mode.value">{{ mode.label }}</el-button></div>
  <div v-if="kind === 'tcping' || kind === 'https' || kind === 'quality'" class="network-test-options"><label for="network-test-target">目标地址</label><el-input id="network-test-target" v-model="target" :disabled="busy" placeholder="host:port，例如 www.example.com:443" @keyup.enter="run" /></div>
  <div v-if="kind === 'mtr'" class="network-test-options"><label>探测方式</label><el-radio-group v-model="mtrProtocol" :disabled="busy"><el-radio-button value="tcp">TCP · 节点端口</el-radio-button><el-radio-button value="icmp">ICMP</el-radio-button></el-radio-group></div>
  <p class="network-test-hint">{{ kind === 'quality' ? '经固定 SOCKS5 交替请求 1 KB 和 256 KB 响应，默认 8 次。' : kind === 'https' ? '经 SOCKS5 检测指定目标的 TLS 与 HTTP 响应，不影响自动健康判断。' : kind === 'tcping' ? '经 SOCKS5 节点测试 TCP 建连。' : kind === 'protocols' ? '经 SOCKS5 节点检测 TCP、UDP 和 DNS。' : '测试 S2L 服务器到节点的网络路径。' }}</p>
  <div v-if="result?.quality" class="quality-results"><p>成功 {{result.quality.successes}} / {{result.quality.samples.length}} · 中位数 {{Math.round(result.quality.median_ms)}} ms · P95 {{Math.round(result.quality.p95_ms)}} ms</p><article v-for="(sample,i) in result.quality.samples" :key="i"><strong>{{i+1}} · {{sample.requested/1024}} KB 请求</strong><p>首字节 {{Math.round(sample.first_byte_ms)}} ms · 总耗时 {{Math.round(sample.duration_ms)}} ms · 收到 {{sample.received}} B</p><p>{{sample.error || (sample.bounded?'达到读取上限':sample.complete?'响应完整':'响应未完成')}} · HTTP {{sample.status}}</p></article><p class="muted">{{result.quality.scope}}</p></div>
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
  <div v-if="busy || result" class="network-test-result" aria-live="polite" :aria-busy="busy">
   <div class="network-test-summary"><el-tag :type="busy || stopped ? 'info' : result?.ok ? 'success' : 'warning'">{{ summary }}</el-tag><span v-if="result?.target">{{ result.target }}</span></div>
   <div v-if="rows.length" class="network-protocols"><article v-for="row in rows" :key="row.name" class="network-protocol"><div><strong>{{ row.name }}</strong><el-tag :type="row.state === 'healthy' ? 'success' : 'warning'" size="small">{{ labels[row.state] || row.state }}</el-tag></div><span class="network-test-hint">{{ methods[row.method] || row.method }}</span><p>{{protocolSummary(row)}}</p><p v-if="row.error && !(row.state === 'restricted' && row.status_code)">{{ row.error }}</p><details v-if="row.attempts?.length"><summary>分目标结果</summary><p v-for="attempt in row.attempts" :key="attempt.target">{{attempt.target}} · {{protocolSummary(attempt)}}</p></details></article></div>
   <template v-if="result?.kind === 'mtr'">
    <p v-if="busy && !hops.length" class="network-test-hint">正在等待跳点响应…</p>
    <div v-if="hops.length" class="network-hops"><article v-for="hop in hops" :key="hop.hop" class="network-hop"><span class="network-hop-number">{{ hop.hop }}</span><div><strong>{{ hop.address }}</strong><small>响应 {{ hop.received }}<template v-if="hop.sent"> / {{ hop.sent }}</template></small></div><div class="network-hop-latency"><strong>{{ hop.average }} <small>ms</small></strong><small>平均 · 最新 {{ hop.last }} ms</small></div></article></div>
    <p v-if="!busy && !hops.length && !result.error && !error" class="network-test-hint">未收到跳点响应。</p>
    <p class="network-test-hint">中间跳不响应不等于业务丢包。</p>
   </template>
   <template v-if="result?.samples"><p>建连成功 {{ result.successes }} / {{ result.attempts }}</p><ol class="network-samples"><li v-for="(sample,index) in result.samples" :key="index">{{ sample.error || `${sample.milliseconds.toFixed(2)} ms` }}</li></ol></template>
   <el-alert v-if="result?.error" :title="result.error" type="warning" :closable="false" />
   <p v-if="result?.output_truncated" class="network-test-hint">输出已达 32 KiB 上限。</p>
   <details v-if="result?.output && result.kind === 'mtr'" class="network-raw"><summary>原始输出</summary><pre>{{ result.output }}</pre></details>
   <pre v-else-if="result?.output" class="network-raw-output">{{ result.output }}</pre>
  </div>
  <template #footer><div class="network-test-footer"><el-button v-if="result && !busy" @click="download">导出</el-button><el-button v-if="busy" type="warning" @click="controller?.abort()">停止测试</el-button><el-button v-else type="primary" @click="run">{{ result ? '重新测试' : '开始测试' }}</el-button></div></template>
 </el-dialog>
</template>
<style>
.network-test-dialog.el-dialog{max-width:calc(100vw - 32px);max-height:calc(100vh - 48px);max-height:calc(100dvh - 48px);margin:24px auto;display:flex;flex-direction:column;overflow:hidden;box-sizing:border-box;}
.network-test-dialog .el-dialog__header{flex:none;margin:0;padding-bottom:14px;}
.network-test-dialog .el-dialog__body{flex:1;min-height:0;overflow-y:auto;overscroll-behavior:contain;max-height:none;padding-bottom:20px;}
.network-test-dialog .el-dialog__footer{flex:none;padding-top:14px;}
.network-test-node{display:flex;flex-wrap:wrap;gap:4px 16px;margin-bottom:16px;overflow-wrap:anywhere;}
.network-test-node span,.network-test-hint{color:var(--el-text-color-secondary);font-size:13px;line-height:1.6;}
.network-test-modes{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:8px;}
.network-test-modes .el-button{margin:0;min-width:0;padding:10px 4px;}
.network-test-options{display:grid;gap:8px;margin-top:16px;}
.network-test-options>label{font-size:13px;font-weight:500;}
.network-test-result{margin-top:16px;min-width:0;}
.network-test-summary{display:flex;gap:10px;align-items:center;flex-wrap:wrap;overflow-wrap:anywhere;margin-bottom:16px;}
.network-test-summary>span:last-child{font-size:12px;color:var(--el-text-color-secondary);}
.network-protocols{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px;}
.network-protocol{padding:12px;border:1px solid var(--el-border-color-light);border-radius:8px;min-width:0;}
.network-protocol>div{display:flex;justify-content:space-between;align-items:center;gap:8px;margin-bottom:6px;}
.network-protocol p{overflow-wrap:anywhere;font-size:12px;margin:8px 0 0;}
.network-hop{display:flex;gap:12px;align-items:center;padding:12px 0;border-bottom:1px solid var(--el-border-color-lighter);}
.network-hop-number{flex:none;width:24px;color:var(--el-text-color-secondary);font-size:12px;}
.network-hop>div{min-width:0;flex:1;display:grid;gap:4px;}
.network-hop strong{font-size:13px;font-weight:500;overflow-wrap:anywhere;}
.network-hop small{font-size:11px;color:var(--el-text-color-secondary);font-weight:400;}
.network-hop .network-hop-latency{flex:none;text-align:right;max-width:46%;}
.network-samples{padding-left:24px;line-height:1.9;}
.network-raw{margin-top:12px;font-size:13px;}
.network-raw summary{cursor:pointer;}
.network-raw pre,.network-raw-output{max-height:280px;overflow:auto;white-space:pre;font-size:12px;line-height:1.6;padding:12px;background:var(--el-fill-color-light);border-radius:6px;}
.network-test-footer{display:flex;justify-content:flex-end;gap:10px;}
.network-test-footer .el-button{margin:0;min-height:40px;}
@media(max-width:600px){
 .network-test-dialog.el-dialog.is-fullscreen{width:100%;max-width:none;height:100vh;height:100dvh;max-height:100vh;max-height:100dvh;margin:0;border-radius:0;padding:16px;}
 .network-test-dialog .el-dialog__header{padding-top:env(safe-area-inset-top,0px);padding-right:32px;}
 .network-test-dialog .el-dialog__footer{padding-bottom:env(safe-area-inset-bottom,0px);}
 .network-protocols{grid-template-columns:1fr;}
 .network-test-footer .el-button{flex:1;}
 .network-hop{gap:8px;}
}
@media(max-width:360px){.network-test-modes{grid-template-columns:repeat(2,minmax(0,1fr));}.network-test-modes .el-button{min-height:44px;}}
</style>

<style scoped>.quality-results{overflow-wrap:anywhere}.quality-results article{padding:8px 0;border-bottom:1px solid var(--el-border-color-lighter)}</style>

<script setup>
import { computed, ref, watch, onBeforeUnmount } from 'vue';
import { api } from '../api/client';
import DebugAnalysis from './DebugAnalysis.vue';
const props = defineProps({active:Boolean});
const state = ref({enabled:false,bytes:0,session_id:0});
const busy = ref(false), error = ref('');
const status = computed(() => state.value.enabled ? '采集中' : ({expired:'已到期',manual:'已停止'}[state.value.stop_reason] || '未开启'));
const hasSession = computed(() => !!state.value.session_id);
let timer, controller, version=0, disposed=false;
function stopPolling() {version++;clearTimeout(timer);controller?.abort();}
function schedule() {if(props.active && !disposed) timer=setTimeout(refresh,5000);}
async function refresh() {
 stopPolling(); const current=version; controller=new AbortController();
 try {const value=await api.debug(controller.signal);if(current===version) {state.value=value;error.value='';}}
 catch(e) {if(current===version && e.name!=='AbortError') error.value=e.message;}
 finally {if(current===version && !busy.value) schedule();}
}
async function perform(action) {
 if(busy.value)return;
 stopPolling(); busy.value=true;error.value='';
 try {await action();}catch(e) {error.value=e.message;}
 finally {busy.value=false;schedule();}
}
function toggle() {return perform(async()=>{state.value=await api.setDebug(!state.value.enabled);});}
function download() {return perform(async()=>{await api.exportDebug();state.value=await api.debug();});}
function time(value) {return value && !value.startsWith('0001-') ? new Date(value).toLocaleString() : '—';}
watch(()=>props.active,active=>{stopPolling();if(active&&!busy.value)refresh();},{immediate:true});
onBeforeUnmount(()=>{disposed=true;stopPolling();});
</script>
<template>
 <el-card shadow="never" class="settings-card debug-settings">
  <template #header><div class="card-heading"><span>Debug 日志</span><el-tag :type="state.enabled?'success':'info'">{{ status }}</el-tag></div></template>
  <p class="muted">关闭时不记录排障日志。开启后采集 30 分钟，最多保留最近 4 MiB。</p>
  <el-alert v-if="error" :title="error" type="error" :closable="false" class="section-gap" />
  <div class="debug-summary" v-if="hasSession">
   <div><span>已保留</span><strong>{{ (state.bytes/1024).toFixed(0) }} KiB</strong></div>
   <div><span>开始时间</span><strong>{{ time(state.started_at) }}</strong></div>
   <div><span>{{ state.enabled?'自动停止':'停止时间' }}</span><strong>{{ time(state.enabled?state.expires:state.stopped_at) }}</strong></div>
  </div>
  <el-alert v-if="state.dropped_bytes" :title="`容量已满，已淘汰 ${(state.dropped_bytes/1024).toFixed(0)} KiB 早期日志；导出仅含保留部分。`" type="warning" :closable="false" class="section-gap" />
  <div class="actions section-gap">
   <el-button :type="state.enabled?'warning':'primary'" :loading="busy" @click="toggle">{{ state.enabled?'停止采集':hasSession?'重新采集':'开启 Debug' }}</el-button>
   <el-button :disabled="busy || !hasSession" @click="download">导出日志</el-button>
   <el-button :disabled="busy" @click="refresh">刷新状态</el-button>
  </div>
  <p v-if="hasSession && !state.enabled" class="muted">已停止采集，仍可导出。重新采集会清空本次内容。</p>
  <details class="debug-help"><summary>采集说明</summary>
   <p>日志仅存内存，服务重启后清空。导出包含网络地址与访问目标，分享前请检查。</p>
   <p>健康状态每 5 秒采样；保留最近 512 条事件和 360 次资源采样。时间显示为浏览器本地时间，日志保存为 UTC。</p>
   <p>已运行隧道按原日志级别采集；重启映射后才应用当前 Debug 级别。</p>
  </details>
  <div class="debug-analysis"><DebugAnalysis :key="state.session_id" :disabled="!hasSession || busy" /></div>
 </el-card>
</template>
<style scoped>
.debug-summary {display:flex;gap:16px 32px;flex-wrap:wrap;margin:16px 0;}
.debug-summary div {display:grid;gap:6px;}
.debug-summary span {color:var(--el-text-color-secondary);font-size:12px;}
.debug-summary strong {font-size:14px;font-weight:500;}
.debug-help {color:var(--el-text-color-secondary);font-size:13px;line-height:1.7;}
.debug-help summary {cursor:pointer;width:fit-content;}
.debug-analysis {margin-top:18px;border-top:1px solid var(--el-border-color-lighter);padding-top:18px;}
</style>

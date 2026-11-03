<script setup>
import {ref,watch,onBeforeUnmount} from 'vue';
import {api} from '../api/client';
import {tr,dateTime} from '../i18n/index.js';
const props=defineProps({active:Boolean});
const state=ref({enabled:true,busy:false,reports:[]}),mappings=ref([]),mappingID=ref(''),busy=ref(false),error=ref(''),loaded=ref(false);
const reasons={'upstream':'上游异常','dns':'DNS 异常','live-dns':'DNS 异常','local-rules':'转发规则异常','local-check':'本地检查异常','local-process':'隧道进程异常','ppp-disconnected':'PPP 断线','manual':'手动取证'};
function eventTime(report){return report.triggered_at&&!report.triggered_at.startsWith('0001-')?report.triggered_at:report.started_at;}
function finished(report){return report.finished_at&&!report.finished_at.startsWith('0001-');}
let timer,controller,disposed=false,version=0;
function stop(){version++;clearTimeout(timer);controller?.abort();}
function schedule(){if(props.active&&!disposed)timer=setTimeout(refresh,5000);}
async function refresh(){
 stop();const current=version;controller=new AbortController();
 try{const [view,rows]=await Promise.all([api.evidence(controller.signal),api.listMappings(controller.signal)]);if(current===version){state.value=view;loaded.value=true;mappings.value=rows.filter(m=>m.status==='running');if(!mappings.value.some(m=>m.id===mappingID.value))mappingID.value=mappings.value[0]?.id||'';error.value='';}}
 catch(e){if(current===version&&e.name!=='AbortError')error.value=e.message;}
 finally{if(current===version&&!busy.value)schedule();}
}
async function perform(action){if(busy.value)return;stop();busy.value=true;error.value='';try{await action();}catch(e){error.value=e.message;}finally{busy.value=false;schedule();}}
function toggle(){return perform(async()=>{state.value=await api.setEvidence({enabled:!state.value.enabled});});}
function capture(){return perform(async()=>{state.value=await api.setEvidence({mapping_id:mappingID.value});});}
function download(id){return perform(()=>api.exportEvidence(id));}
watch(()=>props.active,active=>{stop();if(active&&!busy.value)refresh();},{immediate:true});
onBeforeUnmount(()=>{disposed=true;stop();});
</script>
<template>
 <el-card shadow="never" class="settings-card fault-evidence">
  <template #header><div class="card-heading"><span>{{tr("故障自动取证")}}</span><el-tag :type="loaded&&state.enabled?'success':'info'">{{!loaded?tr("读取中"):state.busy?tr("采集中"):state.enabled?tr("已开启"):tr("已关闭")}}</el-tag></div></template>
  <p class="muted">{{tr("异常时自动保存路由、会话、DNS 状态、系统日志和短时抓包，服务重启后仍可导出。")}}</p>
  <el-alert v-if="error||state.warning" :title="error||state.warning" type="warning" :closable="false" />
  <p v-if="loaded&&!state.packet_capture_available" class="muted">{{tr("抓包不可用：请在服务器安装 tcpdump；其余取证正常运行。")}}</p>
  <p v-if="state.queued" class="muted">{{tr("另有 {0} 条故障等待取证",state.queued)}}</p>
  <div class="actions section-gap"><el-button :loading="busy" :disabled="!loaded" @click="toggle">{{state.enabled?tr("关闭自动取证"):tr("开启自动取证")}}</el-button><el-button :disabled="busy" @click="refresh">{{tr("刷新状态")}}</el-button></div>
  <div class="fault-capture section-gap"><el-select v-model="mappingID" :disabled="!loaded||busy" :placeholder='tr("选择运行中的映射")' :aria-label='tr("选择运行中的映射")'><el-option v-for="m in mappings" :key="m.id" :value="m.id" :label="m.l2tp_user+' · '+m.tun_name"/></el-select><el-button :disabled="!loaded||busy||state.queued>=4||!state.enabled||!mappingID" @click="capture">{{tr("记录当前现场")}}</el-button></div>
  <p class="muted">{{tr("抓包最多 20 秒或 4096 个包，仅保留 IPv4 报文头。每条映射冷却 10 分钟，最多保留 8 份；不触发重启或切换出口。")}}</p>
  <div v-for="report in state.reports" :key="report.id" class="fault-report"><div><strong>{{tr(reasons[report.reason]||report.reason)}}</strong><span>{{dateTime(eventTime(report))}} · {{report.mapping_id}}</span><small>{{finished(report)?tr("已保存"):tr("采集中或采集被中断")}}</small><small v-if="finished(report)&&report.capture.startsWith('unavailable')">{{tr("本次未取得抓包，其他现场已保存")}}</small></div><el-button :disabled="busy" @click="download(report.id)">{{tr("导出")}}</el-button></div>
  <p v-if="!state.reports?.length" class="muted">{{tr("暂无故障记录。客户端异常但后台正常时，可手动记录现场。")}}</p>
  <p class="muted">{{tr("取证包包含网络地址，分享前请检查。仅能观察服务器侧，无法替代客户端或 iWAN 抓包。")}}</p>
 </el-card>
</template>
<style scoped>
.fault-capture{display:flex;gap:8px;flex-wrap:wrap}.fault-capture .el-select{flex:1;min-width:180px}.fault-report{display:flex;align-items:center;gap:12px;padding:12px 0;border-top:1px solid var(--el-border-color-lighter)}.fault-report>div{flex:1;min-width:0;display:grid;gap:6px;overflow-wrap:anywhere}.fault-report span,.fault-report small{font-size:14px;color:var(--el-text-color-secondary)}.fault-report strong{font-size:14px;font-weight:500}
</style>

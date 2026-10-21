<script setup>
import { tr } from '../i18n/index.js';

import {computed,ref,watch,onBeforeUnmount} from 'vue';
import {ElMessageBox} from 'element-plus';
import {api} from '../api/client';
const props=defineProps({active:Boolean});
const info=ref({current:{},job:{},release:{}}), busy=ref(false), error=ref('');
const running=computed(()=>['queued','running'].includes(info.value.job?.status));
const phases={get preparing() { return tr("准备更新"); },get building() { return tr("编译程序"); },get backup() { return tr("备份与停止服务"); },get installing() { return tr("安装更新"); },get restarting() { return tr("重启服务"); },get verifying() { return tr("验证服务"); },get rolling_back() { return tr("执行失败恢复"); }};
const statuses={get queued() { return tr("已排队"); },get running() { return tr("更新中"); },get succeeded() { return tr("更新完成"); },get failed() { return tr("更新失败"); },get interrupted() { return tr("任务已中断"); }};
const sameVersion=computed(()=>{const r=info.value.release,c=info.value.current;return r?.commit && !c?.modified && (c?.revision===r.commit || (c?.version===r.tag && !c?.revision));});
const olderRelease=computed(()=>{
 const a=/^v(\d+)\.(\d+)\.(\d+)$/.exec(info.value.current?.version||''),b=/^v(\d+)\.(\d+)\.(\d+)$/.exec(info.value.release?.tag||'');
 if(!a||!b)return false;
 for(let i=1;i<=3;i++){if(+a[i]!==+b[i])return +a[i]>+b[i];}return false;
});
let timer,controller,version=0,disposed=false;
function stop(){version++;clearTimeout(timer);controller?.abort();}
function schedule(){if(props.active&&!disposed)timer=setTimeout(load,5000);}
async function load(){
 stop();const ticket=version;controller=new AbortController();
 try{const result=await api.updateStatus(controller.signal);if(ticket===version){info.value=result;error.value='';}}
 catch(e){if(ticket===version&&e.name!=='AbortError')error.value=running.value?tr("网页暂时不可达，正在重连；更新失败时可能需要通过 SSH 检查服务。"):e.message;}
 finally{if(ticket===version&&!busy.value)schedule();}
}
async function perform(body){
 if(busy.value)return;
 stop();busy.value=true;error.value='';
 try{info.value=await api.updateSoftware(body);}
 catch(e){error.value=e.message;}
 finally{busy.value=false;schedule();}
}
async function start(){
 const release=info.value.release;
 if(!release?.commit||running.value||busy.value||sameVersion.value||olderRelease.value)return;
 const body={action:'start',tag:release.tag,commit:release.commit};
 try{await ElMessageBox.confirm(tr("更新到 {0}？更新会备份现有配置，短暂中断网页和客户端连接；完成后需重新拨号。", release.tag), tr("确认更新"),{type:'warning',get confirmButtonText() { return tr("开始更新"); },get cancelButtonText() { return tr("取消"); }});}
 catch{return;}
 await perform(body);
}
function reload(){window.location.reload();}
watch(()=>props.active,active=>{stop();if(active&&!busy.value)load();},{immediate:true});
onBeforeUnmount(()=>{disposed=true;stop();});
</script>
<template>
 <el-card shadow="never" class="settings-card">
  <template #header><div class="card-heading"><span>{{ tr("软件更新") }}</span><el-tag v-if="running" type="warning">{{ tr("更新中") }}</el-tag></div></template>
  <p>{{ tr("当前版本：") }}<strong>{{ info.current?.version || tr("未知") }}</strong><span v-if="info.current?.revision"> · {{ info.current.revision.slice(0,12) }}</span><span v-if="info.current?.modified"> {{ tr("· 含本地修改") }}</span></p>
  <p v-if="info.release?.tag">{{ tr("最新稳定版：") }}<el-link :href="info.release.url" target="_blank" rel="noopener noreferrer">{{ info.release.tag }}</el-link><span v-if="sameVersion"> {{ tr("· 已是该版本") }}</span><span v-else-if="olderRelease"> {{ tr("· 当前版本较新") }}</span></p>
  <el-alert v-if="info.available===false" :title="info.message" type="info" :closable="false" class="section-gap" />
  <el-alert v-if="error" :title="error" type="error" :closable="false" class="section-gap" />
  <div v-if="info.job?.id" class="update-status">
   <el-tag :type="info.job.status==='succeeded'?'success':['failed','interrupted'].includes(info.job.status)?'danger':'warning'">{{ statuses[info.job.status] || info.job.status }}</el-tag>
   <span>{{ info.job.tag }} · {{ info.job.status === 'succeeded' ? tr("服务已通过验证") : (running ? '' : tr("最后阶段：")) + (phases[info.job.phase] || tr("准备更新")) }}</span>
   <p v-if="info.job.message">{{ tr(info.job.message) }}</p>
  </div>
  <div class="actions">
   <el-button :loading="busy" :disabled="running || !info.available" @click="perform({action:'check'})">{{ tr("检查更新") }}</el-button>
   <el-button type="primary" :disabled="busy || running || !info.available || !info.release?.commit || sameVersion || olderRelease" @click="start">{{ tr("更新到稳定版") }}</el-button>
   <el-button v-if="info.job?.status==='succeeded'" @click="reload">{{ tr("刷新页面") }}</el-button>
  </div>
  <p class="muted">{{ tr("后台更新并保留配置，关闭网页不影响进度。") }}</p>
  <details class="muted"><summary>{{ tr("更新说明") }}</summary><p>{{ tr("编译成功后才停止服务；失败沿用现有恢复流程。若网页持续不可达，请通过 SSH 检查 s2l、s2l-guard 与 xl2tpd 服务及升级备份。页面保存的只是更新状态，不记录命令输出或排障日志。") }}</p></details>
 </el-card>
</template>
<style scoped>
.update-status{margin:16px 0;line-height:1.8;}.update-status>span{margin-right:12px;}
</style>

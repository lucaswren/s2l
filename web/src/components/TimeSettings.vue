<script setup>
import { onMounted, ref, reactive } from 'vue';
import { api } from '../api/client';
const info = ref(null), busy = ref(false), error = ref(''), message = ref('');
const form = reactive({timezone:'Asia/Shanghai', auto_sync:true, mode:'system'});
const servers = ref('');
async function load() {
 busy.value=true; error.value='';
 try { info.value=await api.timeSettings(); Object.assign(form,{timezone:info.value.timezone,auto_sync:info.value.auto_sync,mode:info.value.mode}); servers.value=info.value.servers.join('\n'); }
 catch(e) { error.value=e.message; } finally { busy.value=false; }
}
async function save() {
 busy.value=true; error.value=''; message.value='';
 try {const result=await api.saveTimeSettings({...form,servers:form.mode==='custom'?servers.value.split(/[\s,]+/).filter(Boolean):[]}); message.value=result.message; await load();}
 catch(e) {error.value=e.message;} finally {busy.value=false;}
}
function serverTime() {
 try {return new Intl.DateTimeFormat('zh-CN',{timeZone:info.value.timezone,dateStyle:'full',timeStyle:'long'}).format(new Date(info.value.server_time));}
 catch {return info.value.server_time;}
}
onMounted(load);
</script>
<template>
 <el-card shadow="never" class="settings-card">
  <template #header>时区与时间同步</template>
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
  <el-alert v-if="message" :title="message" type="success" :closable="false" />
  <template v-if="info">
   <p>服务器时间：{{ serverTime() }} · {{ info.timezone }}</p>
   <p>自动同步：{{ info.auto_sync?'已开启':'已关闭' }}；系统时钟：{{ info.synchronized?'已同步':'尚未确认同步' }}<span v-if="info.active_server">；当前 NTP：{{ info.active_server }}</span></p>
   <el-form label-position="top" :disabled="busy" @submit.prevent="save">
    <el-form-item label="服务器时区"><el-select v-model="form.timezone" filterable style="width:100%"><el-option v-for="zone in info.timezones" :key="zone" :label="zone" :value="zone" /></el-select></el-form-item>
    <el-form-item label="自动同步"><el-switch v-model="form.auto_sync" /></el-form-item>
    <el-form-item label="NTP 服务器"><el-radio-group v-model="form.mode"><el-radio value="system">系统默认</el-radio><el-radio value="custom" :disabled="!info.custom_available">自定义 NTP</el-radio></el-radio-group></el-form-item>
    <el-form-item v-if="form.mode==='custom'" label="NTP 服务器（每行一个，最多 8 个）"><el-input v-model="servers" type="textarea" :rows="3" placeholder="输入可访问的 NTP 服务器域名或 IP" /></el-form-item>
    <details class="muted time-help"><summary>同步说明</summary><p>自定义 NTP 需使用 systemd-timesyncd，不能与 chrony/ntpd 混用。时间源需能通过 UDP 123 访问；网络接口提供的时间源仍可能生效，以当前 NTP 状态为准。</p><p>已开启自动同步不代表已完成同步。日志统一保存为 UTC，服务器时间为最近刷新结果。</p></details>
    <el-button type="primary" native-type="submit" :loading="busy">保存时间设置</el-button>
   </el-form>
  </template>
  <el-button :disabled="busy" style="margin-top:12px" @click="load">刷新同步状态</el-button>
 </el-card>
</template>

<style scoped>
.time-help {margin-bottom:16px;font-size:13px;line-height:1.7;}
.time-help summary {cursor:pointer;}
</style>

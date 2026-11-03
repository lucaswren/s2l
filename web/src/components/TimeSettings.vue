<script setup>
import { tr, language } from '../i18n/index.js';

import { computed, onMounted, ref, reactive } from 'vue';
import { api } from '../api/client';
import { mergeSettingsDraft } from '../utils/settings-draft';
import { timezoneGroups } from '../api/timezones';
const info = ref(null), busy = ref(false), error = ref(''), message = ref('');
const form = reactive({timezone:'Asia/Shanghai', auto_sync:true, mode:'system'});
const servers = ref('');
const zoneGroups = computed(() => timezoneGroups(info.value?.timezones, info.value?.timezone, info.value?.server_time));
function draft(value) { return value ? {timezone:value.timezone, auto_sync:value.auto_sync, mode:value.mode, servers:(value.servers || []).join('\n')} : null; }
async function load(reset = false) {
 busy.value=true; error.value='';
 try { const next=await api.timeSettings();const merged=mergeSettingsDraft({...form,servers:servers.value},reset === true ? null : draft(info.value),draft(next));Object.assign(form,{timezone:merged.timezone,auto_sync:merged.auto_sync,mode:merged.mode});servers.value=merged.servers;info.value=next; }
 catch(e) { error.value=e.message; } finally { busy.value=false; }
}
async function save() {
 busy.value=true; error.value=''; message.value='';
 try {const result=await api.saveTimeSettings({...form,servers:form.mode==='custom'?servers.value.split(/[\s,]+/).filter(Boolean):[]}); message.value=result.message; await load(true);}
 catch(e) {error.value=e.message;} finally {busy.value=false;}
}
function serverTime() {
 try {return new Intl.DateTimeFormat(language.value,{timeZone:info.value.timezone,dateStyle:'full',timeStyle:'long'}).format(new Date(info.value.server_time));}
 catch {return info.value.server_time;}
}
onMounted(load);
</script>
<template>
 <el-card shadow="never" class="settings-card">
  <template #header>{{ tr("时区与时间同步") }}</template>
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
  <el-alert v-if="message" :title="tr(message)" type="success" :closable="false" />
  <template v-if="info">
   <p>{{ tr("服务器时间：") }}{{ serverTime() }} · {{ info.timezone }}</p>
   <p>{{ tr("自动同步：") }}{{ info.auto_sync?tr("已开启"):tr("已关闭") }}{{ tr("；系统时钟：") }}{{ info.synchronized?tr("已同步"):tr("尚未确认同步") }}<span v-if="info.active_server">{{ tr("；当前 NTP：") }}{{ info.active_server }}</span></p>
   <el-form label-position="top" :disabled="busy" @submit.prevent="save">
    <el-form-item :label='tr("服务器时区")'><el-select v-model="form.timezone" filterable :placeholder='tr("搜索城市、时区或 UTC 偏移")' style="width:100%"><el-option-group v-for="group in zoneGroups" :key="group.label" :label="group.label"><el-option v-for="zone in group.options" :key="zone.value" :label="zone.label" :value="zone.value" /></el-option-group></el-select></el-form-item>
    <p class="muted zone-help">{{ tr("UTC 偏移按服务器当前日期显示，夏令时地区会随日期变化。") }}</p>
    <el-form-item :label='tr("自动同步")'><el-switch v-model="form.auto_sync" /></el-form-item>
    <el-form-item :label='tr("NTP 服务器")'><el-radio-group v-model="form.mode"><el-radio value="system">{{ tr("系统默认") }}</el-radio><el-radio value="custom" :disabled="!info.custom_available">{{ tr("自定义 NTP") }}</el-radio></el-radio-group></el-form-item>
    <el-form-item v-if="form.mode==='custom'" :label='tr("NTP 服务器（每行一个，最多 8 个）")'><el-input v-model="servers" type="textarea" :rows="3" :placeholder='tr("输入可访问的 NTP 服务器域名或 IP")' /></el-form-item>
    <details class="muted time-help"><summary>{{ tr("同步说明") }}</summary><p>{{ tr("自定义 NTP 需使用 systemd-timesyncd，不能与 chrony/ntpd 混用。时间源需能通过 UDP 123 访问；网络接口提供的时间源仍可能生效，以当前 NTP 状态为准。") }}</p><p>{{ tr("已开启自动同步不代表已完成同步。日志统一保存为 UTC，服务器时间为最近刷新结果。") }}</p></details>
    <el-button type="primary" native-type="submit" :loading="busy">{{ tr("保存时间设置") }}</el-button>
   </el-form>
  </template>
  <el-button :disabled="busy" style="margin-top:12px" @click="load(false)">{{ tr("刷新同步状态") }}</el-button>
 </el-card>
</template>

<style scoped>
.zone-help {margin-top:-12px;font-size:14px;}
.time-help {margin-bottom:16px;font-size:14px;line-height:1.7;}
.time-help summary {cursor:pointer;}
</style>

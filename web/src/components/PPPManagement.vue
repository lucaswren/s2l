<script setup>
import { tr } from '../i18n/index.js';

import {ref,onMounted,onBeforeUnmount} from 'vue';
import {api} from '../api/client';
import {managementAddresses} from '../utils/management-address';
const addresses=ref([]),automatic=ref(false);
const info=ref({}),access=ref({enabled:false,destination_ip:'',source_cidrs:[],tcp_ports:[]}),ports=ref(''),sources=ref(''),busy=ref(false),error=ref(''),message=ref('');
let disposed=false;
onBeforeUnmount(()=>{disposed=true});
onMounted(async()=>{try{const v=await api.pppManagement();if(disposed)return;info.value=v;const choice=managementAddresses(v,location.hostname);addresses.value=choice.addresses;automatic.value=choice.automatic;access.value={...access.value,...v.access,destination_ip:choice.selected};ports.value=(v.access.tcp_ports?.length?v.access.tcp_ports:v.detected_ports||[]).join(',');sources.value=(v.access.source_cidrs?.length?v.access.source_cidrs:v.sources?.length===1?[v.sources[0].subnet]:[]).join('\n');}catch(e){if(!disposed)error.value=e.message;}});
function detected(){ports.value=[...new Set(info.value.detected_ports||[])].join(',')}
async function save(){if(busy.value)return;busy.value=true;error.value='';message.value='';try{
 const p=ports.value.split(/[,，\s]+/).filter(Boolean);if(p.some(x=>!/^\d+$/.test(x)))throw Error(tr("端口需为数字，以逗号分隔"));
 const body={...access.value,tcp_ports:p.map(Number),source_cidrs:sources.value.split(/[\s,，]+/).filter(Boolean)};
 const v=await api.savePPPManagement(body);if(!disposed){access.value=v.access;message.value=v.message;}
}catch(e){if(!disposed)error.value=e.message;}finally{if(!disposed)busy.value=false}}
</script>
<template>
 <el-card shadow="never" class="settings-card ppp-management"><template #header>{{ tr("L2TP 内管理访问") }}</template>
  <el-alert v-if="error" :title="error" type="error" :closable="false" /><el-alert v-if="message" :title="tr(message)" type="success" :closable="false" />
  <p class="muted">{{ tr("允许指定来源访问本机管理端口，默认关闭。") }}</p>
  <el-form label-position="top" :disabled="busy || !info.available">
   <el-form-item :label='tr("启用管理例外")'><el-switch v-model="access.enabled" /></el-form-item>
   <el-form-item :label='tr("服务器本机 IPv4")'><el-select v-model="access.destination_ip" filterable allow-create default-first-option :placeholder="tr('选择服务器地址')" @change="automatic=false"><el-option v-for="ip in addresses" :key="ip" :label="ip" :value="ip" /></el-select><span v-if="automatic" class="field-hint">{{ tr('已自动填入本机地址') }}</span></el-form-item>
   <el-form-item :label='tr("允许的 PPP 来源网段")'><el-input v-model="sources" type="textarea" :rows="2" :placeholder='tr("每行一个，例如 10.0.101.0/24")' /></el-form-item>
   <p v-if="info.sources?.length" class="muted">{{ tr("已有映射：") }}{{info.sources.map(m=>`${m.name} (${m.subnet})`).join('；')}}</p>
   <el-form-item :label='tr("允许的 TCP 端口")'><el-input v-model="ports" :placeholder='tr("面板端口、实际 SSH 端口，以逗号分隔")' /></el-form-item>
   <div class="actions"><el-button v-if="info.detected_ports?.length" :disabled="busy" @click="detected">{{ tr("填入检测到的面板及 SSH 端口") }}</el-button>
   <el-button type="primary" :disabled="!info.available" :loading="busy" @click="save">{{ tr("保存管理例外") }}</el-button></div>
  </el-form>
  <details class="management-notes"><summary>{{ tr('检测范围与说明') }}</summary><p v-if="info.port_check_available">{{ tr("配置涉及端口：") }}{{info.configured_ports?.join(', ') || tr("未取得")}}{{ tr("；当前监听：") }}{{info.detected_ports?.join(', ') || tr("未发现匹配端口")}}</p><p class="muted">{{ tr("目标 IP 需绑定本机；公网 NAT 地址需另行核对。移除例外会中断对应管理连接。") }}</p></details>
 </el-card>
</template>
<style scoped>.ppp-management p{overflow-wrap:anywhere}.ppp-management .el-input,.ppp-management .el-select{width:100%}.management-notes{margin-top:20px}.field-hint{margin-top:6px;color:var(--el-text-color-secondary);font-size:13px;line-height:1.5}</style>

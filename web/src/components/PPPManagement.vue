<script setup>
import {ref,onMounted,onBeforeUnmount} from 'vue';
import {api} from '../api/client';
const info=ref({}),access=ref({enabled:false,destination_ip:'',source_cidrs:[],tcp_ports:[]}),ports=ref(''),sources=ref(''),busy=ref(false),error=ref(''),message=ref('');
let disposed=false;
onBeforeUnmount(()=>{disposed=true});
onMounted(async()=>{try{const v=await api.pppManagement();if(disposed)return;info.value=v;access.value={...access.value,...v.access};ports.value=(v.access.tcp_ports?.length?v.access.tcp_ports:v.detected_ports||[]).join(',');sources.value=(v.access.source_cidrs?.length?v.access.source_cidrs:v.sources?.length===1?[v.sources[0].subnet]:[]).join('\n');if(!access.value.destination_ip&&v.local_ipv4?.length===1)access.value.destination_ip=v.local_ipv4[0];}catch(e){if(!disposed)error.value=e.message;}});
function detected(){ports.value=[...new Set(info.value.detected_ports||[])].join(',')}
async function save(){if(busy.value)return;busy.value=true;error.value='';message.value='';try{
 const p=ports.value.split(/[,，\s]+/).filter(Boolean);if(p.some(x=>!/^\d+$/.test(x)))throw Error('端口需为数字，以逗号分隔');
 const body={...access.value,tcp_ports:p.map(Number),source_cidrs:sources.value.split(/[\s,，]+/).filter(Boolean)};
 const v=await api.savePPPManagement(body);if(!disposed){access.value=v.access;message.value=v.message;}
}catch(e){if(!disposed)error.value=e.message;}finally{if(!disposed)busy.value=false}}
</script>
<template>
 <el-card shadow="never" class="settings-card ppp-management"><template #header>L2TP 内管理访问</template>
  <el-alert v-if="error" :title="error" type="error" :closable="false" /><el-alert v-if="message" :title="message" type="success" :closable="false" />
  <p v-if="info.local_ipv4?.length">本机 IPv4：{{info.local_ipv4.join(', ')}}</p><p v-if="info.port_check_available">配置涉及端口：{{info.configured_ports?.join(', ') || '未取得'}}；当前监听：{{info.detected_ports?.join(', ') || '未发现匹配端口'}}</p>
  <p>仅允许指定 PPP 来源访问本机目标 IP 的 TCP 管理端口。默认关闭，其他入站保持阻断。</p>
  <el-form label-position="top" :disabled="busy || !info.available">
   <el-form-item label="启用管理例外"><el-switch v-model="access.enabled" /></el-form-item>
   <el-form-item label="服务器本机 IPv4"><el-input v-model="access.destination_ip" placeholder="例如服务器绑定的公网 IPv4" /></el-form-item>
   <el-form-item label="允许的 PPP 来源网段"><el-input v-model="sources" type="textarea" :rows="2" placeholder="每行一个，例如 10.0.101.0/24" /></el-form-item>
   <p v-if="info.sources?.length" class="muted">已有映射：{{info.sources.map(m=>`${m.name} (${m.subnet})`).join('；')}}</p>
   <el-form-item label="允许的 TCP 端口"><el-input v-model="ports" placeholder="面板端口、实际 SSH 端口，以逗号分隔" /></el-form-item>
   <el-button v-if="info.detected_ports?.length" :disabled="busy" @click="detected">填入检测到的面板及 SSH 端口</el-button>
   <el-button type="primary" :loading="busy" @click="save">保存管理例外</el-button>
  </el-form>
  <p class="muted">目标 IP 必须绑定在服务器本机；云平台公网 NAT 地址需另行核对。关闭例外或移除当前来源/端口会中断此路径的管理连接。此功能不修改 SSH 监听、面板端口或认证设置。</p>
 </el-card>
</template>
<style scoped>.ppp-management p{font-size:13px;line-height:1.7;overflow-wrap:anywhere}.ppp-management .el-input{width:100%}</style>

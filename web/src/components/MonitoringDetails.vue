<script setup>
import { tr, dateTime } from '../i18n/index.js';

import {computed,ref,watch,onBeforeUnmount} from 'vue';
import Operations from './Operations.vue';
import {ElMessageBox} from 'element-plus';
import {api} from '../api/client';
import {passRate} from '../utils/health';
const props=defineProps({status:Object});
const selected=ref(''),history=ref([]),warning=ref(''),busy=ref(false);
const mappings=computed(()=>props.status?.mappings||[]);
const sessions=computed(()=>props.status?.sessions||[]);
let controller,disposed=false,version=0;
watch(mappings,rows=>{if(!rows.some(m=>m.id===selected.value))selected.value=rows[0]?.id||'';},{immediate:true});
async function load(){
 const current=++version;controller?.abort();history.value=[];warning.value='';
 if(!selected.value){busy.value=false;return}
 controller=new AbortController();busy.value=true;
 try{const data=await api.history(selected.value,controller.signal);if(!disposed&&current===version){history.value=data.buckets||[];warning.value=data.warning||'';}}
 catch(e){if(e.name!=='AbortError'&&!disposed&&current===version)warning.value=e.message;}
 finally{if(!disposed&&current===version)busy.value=false;}
}
watch(selected,load,{immediate:true});
const timer=setInterval(()=>{if(!busy.value)load()},60000);
onBeforeUnmount(()=>{disposed=true;controller?.abort();clearInterval(timer)});
const disconnecting=ref('');
async function disconnect(row){if(disconnecting.value)return;try{await ElMessageBox.confirm(tr("断开 {0} 会话？", row.interface),tr("会话管理"),{type:'warning'});disconnecting.value=row.interface;await api.disconnectSession(row)}catch(e){if(e!=='cancel'&&e!=='close')warning.value=e.message}finally{disconnecting.value=''}}
function name(id){const m=mappings.value.find(m=>m.id===id);return m?.tun_name||tr("未匹配映射")}
function time(value){return value?dateTime(value):'—'}
function bytes(value){const n=Number(value)||0;return n>1048576?`${(n/1048576).toFixed(1)} MB`:`${(n/1024).toFixed(1)} KB`}
const totals=computed(()=>history.value.reduce((acc,b)=>{for(const key of ['pass','fail','restricted','unsupported'])acc[key]+=(b.tcp?.[key]||0);return acc},{pass:0,fail:0,restricted:0,unsupported:0}));
const recent=computed(()=>history.value.slice(-12).reverse());
</script>
<template>
 <el-card shadow="never" class="section-gap">
  <template #header>{{ tr("PPP 在线会话") }}</template>
  <el-alert v-if="status.sessions_error" :title="status.sessions_error+tr(&quot;；下方保留上次采集结果&quot;)" type="warning" :closable="false"/>
  <el-table class="desktop-list" :data="sessions" :empty-text='tr("暂无已观测会话")'>
   <el-table-column prop="interface" :label='tr("接口")'/><el-table-column :label='tr("映射")'><template #default="{row}">{{name(row.mapping_id)}}</template></el-table-column>
   <el-table-column prop="peer_ip" :label='tr("隧道地址")'/><el-table-column prop="mtu" :label='tr("实际 MTU")'/>
   <el-table-column :label='tr("拨号时间 / 首次观测")' min-width="175"><template #default="{row}">{{time(row.connected_at&&!row.connected_at.startsWith('0001')?row.connected_at:row.first_seen)}}</template></el-table-column>
   <el-table-column :label='tr("接收 / 发送")' min-width="160"><template #default="{row}">{{bytes(row.rx_bytes)}} / {{bytes(row.tx_bytes)}}</template></el-table-column>
   <el-table-column :label='tr("丢包 / 错误")'><template #default="{row}">{{row.dropped}} / {{row.errors}}<span v-if="row.error"> · {{row.error}}</span></template></el-table-column>
<el-table-column :label='tr("会话管理")' width="110"><template #default="{row}"><el-button v-if="row.disconnectable" size="small" :loading="disconnecting===row.interface" @click="disconnect(row)">{{ tr("断开") }}</el-button></template></el-table-column>
  </el-table>
  <div class="mobile-list"><el-empty v-if="!sessions.length" :description='tr("暂无已观测会话")'/><article v-for="row in sessions" :key="row.index" class="session-row"><strong>{{row.interface}} · {{name(row.mapping_id)}}</strong><p>{{row.peer_ip}} · MTU {{row.mtu}}</p><p>{{ tr("接收") }} {{bytes(row.rx_bytes)}} {{ tr("· 发送") }} {{bytes(row.tx_bytes)}}</p><p>{{ tr("丢包") }} {{row.dropped}} {{ tr("· 错误") }} {{row.errors}}</p><p>{{ tr("首次观测") }} {{time(row.connected_at&&!row.connected_at.startsWith('0001')?row.connected_at:row.first_seen)}}</p><p v-if="row.error">{{row.error}}</p><el-button v-if="row.disconnectable" :loading="disconnecting===row.interface" @click="disconnect(row)">{{ tr("断开会话") }}</el-button></article></div>
  <p class="muted">{{ tr("首次观测时间不等于拨号时间；一条隧道可能承载多台设备。") }}</p>
 </el-card>
 <el-card shadow="never" class="section-gap">
  <template #header>{{ tr("最近 24 小时") }}</template>
  <div class="history-controls"><el-select v-model="selected" :aria-label='tr("历史记录映射")' :placeholder='tr("选择映射")'><el-option v-for="m in mappings" :key="m.id" :label="m.tun_name" :value="m.id"/></el-select><el-button :loading="busy" @click="load">{{ tr("刷新") }}</el-button></div>
  <el-alert v-if="warning" :title="warning" type="warning" :closable="false"/>
  <p>{{ tr("TCP 探测成功率") }} {{passRate(totals)}} {{ tr("· 成功") }} {{totals.pass}} {{ tr("· 失败") }} {{totals.fail}} {{ tr("· 目标限制") }} {{totals.restricted}}</p>
  <el-table class="desktop-list" :data="recent" :empty-text='tr("暂无历史采样")'>
   <el-table-column :label='tr("时段")' min-width="180"><template #default="{row}">{{time(row.at*1000)}}</template></el-table-column>
   <el-table-column :label='tr("TCP / DNS 成功率")' min-width="160"><template #default="{row}">{{passRate(row.tcp)}} / {{passRate(row.dns)}}</template></el-table-column>
   <el-table-column :label='tr("平均 HTTPS 耗时")' min-width="145"><template #default="{row}">{{row.latency_samples?Math.round(row.latency_total_ms/row.latency_samples)+' ms':'—'}}</template></el-table-column>
   <el-table-column :label='tr("恢复 / 进程变化 / 会话变化")' min-width="210"><template #default="{row}">{{row.repairs}} / {{row.restarts}} / {{row.session_changes}}<div v-if="row.forwarding_pass || row.forwarding_fail || row.exit_changes" class="muted">{{ tr("转发实测") }} {{row.forwarding_pass || 0}} {{ tr("成功 /") }} {{row.forwarding_fail || 0}} {{ tr("失败 · 出口变化") }} {{row.exit_changes || 0}}</div></template></el-table-column>
   <el-table-column :label='tr("接收 / 发送峰值")' min-width="170"><template #default="{row}">{{bytes(row.rx_peak_bps)}}/s · {{bytes(row.tx_peak_bps)}}/s</template></el-table-column>
  </el-table>
  <div class="mobile-list"><el-empty v-if="!recent.length" :description='tr("暂无历史采样")'/><article v-for="row in recent.slice(0,6)" :key="row.at" class="session-row"><strong>{{time(row.at*1000)}}</strong><p>TCP {{passRate(row.tcp)}} · DNS {{passRate(row.dns)}}</p><p>{{ tr("恢复") }} {{row.repairs}} {{ tr("· 进程变化") }} {{row.restarts}} {{ tr("· 会话变化") }} {{row.session_changes}}</p><p v-if="row.forwarding_pass || row.forwarding_fail || row.exit_changes">{{ tr("实测") }} {{row.forwarding_pass || 0}} {{ tr("成功 /") }} {{row.forwarding_fail || 0}} {{ tr("失败 · 出口变化") }} {{row.exit_changes || 0}}</p></article></div>
  <p class="muted">{{ tr("每 5 分钟汇总；列表展示最近时段。成功率来自已完成探测，未采样不计为离线。") }}</p>
 </el-card>
<p v-if="Object.keys(status.host_stats||{}).length" class="muted">{{ tr("服务器 TCP 连接") }} {{status.host_stats.CurrEstab ?? '—'}} {{ tr("· 累计重传段") }} {{status.host_stats.RetransSegs ?? '—'}} · conntrack {{status.host_stats.conntrack_count ?? '—'}} / {{status.host_stats.conntrack_max ?? '—'}}{{ tr("（全机统计）") }}</p>
<Operations :mappings="mappings"/>
</template>
<style scoped>
.history-controls{display:flex;gap:12px;margin-bottom:16px;flex-wrap:wrap}.history-controls .el-select{width:240px;max-width:100%}.session-row{padding:12px 0;border-bottom:1px solid var(--el-border-color-lighter);overflow-wrap:anywhere}.session-row p{font-size:13px;margin:8px 0}.session-row:last-child{border:0}
</style>

<script setup>
import { ref, onBeforeUnmount } from 'vue';
import { api } from '../api/client';
const props = defineProps({ node: Object });
const emit = defineEmits(['close', 'changed']);
const kind = ref('protocols');
const labels = { healthy: '正常', degraded: '仅备用可用', unavailable: '不可用', unsupported: '不支持' };
const targets = ['www.example.com:443', '149.154.167.41:443', '91.108.56.139:443'];
const target = ref('www.example.com:443');
const busy = ref(false);
const result = ref(null);
const error = ref('');
let controller;
onBeforeUnmount(() => controller?.abort());
async function run() {
  if (busy.value) return;
  busy.value = true; error.value = ''; result.value = null;
  controller = new AbortController();
  try { result.value = await api.networkTestNode(props.node.id, { kind: kind.value, target: target.value.trim() }, controller.signal); emit("changed"); }
  catch (e) { error.value = e.name === 'AbortError' ? '测试已取消' : e.message; }
  finally { busy.value = false; }
}
function close() { controller?.abort(); emit('close'); }
function download() {
 const blob = new Blob([JSON.stringify(result.value, null, 2)], {type:'application/json'});
 const url = URL.createObjectURL(blob); const a = document.createElement('a'); a.href=url; a.download='s2l-network-test.json'; a.click(); setTimeout(()=>URL.revokeObjectURL(url),1000);
}
</script>
<template>
 <el-dialog :model-value="true" :title="`节点网络测试 · ${node.name}`" width="760px" @close="close">
  <p class="muted">检测服务器到节点及目标的连接；客户端分流、L2TP 和图片传输需另行验证。</p>
  <el-radio-group v-model="kind" :disabled="busy"><el-radio-button value="protocols">协议检测</el-radio-button><el-radio-button value="tcping">TCPing</el-radio-button><el-radio-button value="ping">Ping 节点</el-radio-button><el-radio-button value="mtr">MTR 到节点</el-radio-button></el-radio-group>
  <template v-if="kind === 'tcping'">
   <el-input v-model="target" :disabled="busy" placeholder="目标 host:port" style="margin-top:16px" />
   <div style="margin-top:8px"><el-button v-for="(address, i) in targets" :key="address" size="small" :disabled="busy" @click="target = address">{{ i ? `TG 目标 ${i}` : '通用目标' }}</el-button></div>
   <p class="muted">TG 快捷地址取自此次日志，仅验证 TCP 建连；可填写实际失败的媒体 IP 和端口。</p>
  </template>
  <p v-else-if="kind !== 'protocols'" class="muted">目标：{{ node.addr }}{{ kind === 'mtr' ? '；中间跳不响应不等于业务丢包。' : '' }}</p>
  <p v-if="error" role="alert">{{ error }}</p>
  <div v-if="result" style="margin-top:16px">
   <p>{{ result.path }} · {{ result.target }} · {{ result.ok ? '通过' : '未全部通过' }}</p>
   <el-table v-if="result.protocols" :data="Object.entries(result.protocols).map(([name, value]) => ({ name: name.toUpperCase(), ...value }))">
    <el-table-column prop="name" label="协议" width="85" />
    <el-table-column label="状态" width="120"><template #default="{row}">{{ labels[row.state] || row.state }}</template></el-table-column>
    <el-table-column prop="method" label="检测方式" width="145" />
    <el-table-column prop="error" label="详情" min-width="220" />
   </el-table>
   <p v-if="result.protocols?.udp?.state !== 'healthy' && result.protocols" class="muted">UDP 检测表示节点能力；主动拒绝策略下，TCP 与 DNS 可独立使用。</p>
   <template v-if="result.samples"><p>成功 {{ result.successes }} / {{ result.attempts }}</p><p v-for="(sample, i) in result.samples" :key="i">{{ i + 1 }}：{{ sample.error || `${sample.milliseconds.toFixed(2)} ms` }}</p></template>
   <p v-if="result.output_truncated">输出超过 32 KiB，已截断；导出仅包含显示部分。</p>
   <p v-if="result.error">{{ result.error }}</p>
   <pre v-if="result.output" style="overflow:auto;max-height:360px;white-space:pre-wrap">{{ result.output }}</pre>
  </div>
  <template #footer><el-button v-if="busy" @click="controller?.abort()">取消测试</el-button><el-button v-if="result" @click="download">导出结果</el-button><el-button type="primary" :loading="busy" @click="run">开始测试</el-button></template>
 </el-dialog>
</template>

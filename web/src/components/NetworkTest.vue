<script setup>
import { ref, onBeforeUnmount } from 'vue';
import { api } from '../api/client';
const props = defineProps({ node: Object });
const emit = defineEmits(['close']);
const kind = ref('tcping');
const target = ref('www.example.com:443');
const busy = ref(false);
const result = ref(null);
const error = ref('');
let controller;
onBeforeUnmount(() => controller?.abort());
async function run() {
  busy.value = true; error.value = ''; result.value = null;
  controller = new AbortController();
  try { result.value = await api.networkTestNode(props.node.id, { kind: kind.value, target: target.value.trim() }, controller.signal); }
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
  <p>TCPing 经所选 SOCKS5 节点连接目标；Ping / MTR 从 S2L 服务器测试到节点地址，无法展示节点出口到目标的 ICMP 路径。MTR 中间跳不响应不代表业务丢包。</p>
  <p>结果仅在当前窗口展示，可手动导出；不开启 Debug 不记录排障日志。</p>
  <el-radio-group v-model="kind" :disabled="busy"><el-radio-button value="tcping">TCPing 经节点</el-radio-button><el-radio-button value="ping">Ping 节点</el-radio-button><el-radio-button value="mtr">MTR 到节点</el-radio-button></el-radio-group>
  <el-input v-if="kind === 'tcping'" v-model="target" :disabled="busy" placeholder="目标 host:port，例如 www.example.com:443" style="margin-top:16px" />
  <p v-else>目标：{{ node.addr }}。Linux 服务器需安装 iputils-ping / mtr-tiny 并具备原始套接字权限。</p>
  <p v-if="error" role="alert">{{ error }}</p>
  <div v-if="result" style="margin-top:16px">
   <p>{{ result.path }} · {{ result.target }} · {{ result.ok ? '完成' : '失败或部分失败' }}</p>
   <template v-if="result.samples"><p>成功 {{ result.successes }} / {{ result.attempts }}</p><p v-for="(sample, i) in result.samples" :key="i">{{ i + 1 }}：{{ sample.error || `${sample.milliseconds.toFixed(2)} ms` }}</p></template>
   <p v-if="result.output_truncated">输出超过 32 KiB，已截断；导出仅包含显示部分。</p>
   <p v-if="result.error">{{ result.error }}</p>
   <pre v-if="result.output" style="overflow:auto;max-height:360px;white-space:pre-wrap">{{ result.output }}</pre>
  </div>
  <template #footer><el-button v-if="busy" @click="controller?.abort()">取消测试</el-button><el-button v-if="result" @click="download">导出结果</el-button><el-button type="primary" :loading="busy" @click="run">开始测试</el-button></template>
 </el-dialog>
</template>

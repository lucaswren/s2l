<script setup>
import { ref } from "vue";
import { api } from "../api/client";
defineProps({ disabled: Boolean });
const result = ref(null);
const busy = ref(false);
const error = ref("");
async function load() {
  busy.value = true;
  try { result.value = await api.debugAnalysis(); error.value = ""; }
  catch(e) { error.value = e.message; }
  finally { busy.value = false; }
}
function mib(n) { return n == null ? '—' : (n/1024/1024).toFixed(1) + ' MiB'; }
</script>
<template>
  <el-button :disabled="disabled" :loading="busy" @click="load">分析时间线</el-button>
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
  <template v-if="result">
    <p class="muted">{{ result.notice }}</p>
    <p v-for="finding in result.findings" :key="finding">{{ finding }}</p>
    <p v-if="result.dropped_events" class="muted">已淘汰早期事件 {{ result.dropped_events }} 条，仅展示最近 512 条。</p>
    <el-table :data="result.events.slice(-100).reverse()" max-height="360" empty-text="暂无采样事件">
      <el-table-column label="时间" min-width="170"><template #default="{row}">{{ new Date(row.at).toLocaleString() }}</template></el-table-column>
      <el-table-column prop="mapping_id" label="映射" min-width="140" />
      <el-table-column prop="detail" label="观察" min-width="260" />
    </el-table>
    <details v-if="result.connections?.length" style="margin:16px 0"><summary>连接排障 · {{ result.connections.length }} 条</summary>
     <p class="muted">最近保留的日志观察，不能据此判断传输成功或停滞。源地址可能已被 NAT。</p>
     <p v-if="result.dropped_connections" class="muted">已淘汰 {{ result.dropped_connections }} 条早期连接。</p>
     <el-table :data="result.failed_targets || []" max-height="240"><el-table-column prop="target" label="失败目标" /><el-table-column prop="protocol" label="协议" /><el-table-column prop="count" label="连接数" /></el-table>
     <el-table :data="result.connections" max-height="360"><el-table-column prop="tun_name" label="接口" width="95" /><el-table-column prop="id" label="连接 ID" width="120" /><el-table-column prop="source" label="源" min-width="150" /><el-table-column prop="target" label="目标" min-width="150" /><el-table-column prop="protocol" label="协议" width="70" /><el-table-column prop="stage" label="阶段" min-width="140" /><el-table-column prop="reply_code" label="拒绝码" width="80" /><el-table-column prop="error" label="错误" min-width="260" /></el-table>
    </details>
    <p class="muted">最近 100 条事件、12 次采样；完整数据见导出包。</p>
    <el-table :data="result.resources.slice(-12).reverse()" max-height="260" empty-text="暂无资源采样">
      <el-table-column label="时间" min-width="170"><template #default="{row}">{{ new Date(row.at).toLocaleString() }}</template></el-table-column>
      <el-table-column label="S2L 堆 / RSS" min-width="170"><template #default="{row}">{{ mib(row.resources.heap_bytes) }} / {{ mib(row.resources.rss_bytes) }}</template></el-table-column>
      <el-table-column label="任务 / FD"><template #default="{row}">{{ row.resources.goroutines }} / {{ row.resources.open_fds ?? '—' }}</template></el-table-column>
      <el-table-column label="磁盘可用" min-width="130"><template #default="{row}">{{ row.resources.disk_sampled ? mib(row.resources.disk_free_bytes || 0) : '—' }}</template></el-table-column>
    </el-table>
  </template>
</template>

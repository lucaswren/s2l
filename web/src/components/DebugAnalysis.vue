<script setup>
import { ref } from "vue";
import { api } from "../api/client";
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
  <el-button :loading="busy" @click="load">分析日志时间线</el-button>
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
    <p class="muted">页面显示最近 100 条事件和 12 次资源采样；完整时间线及资源趋势包含在 ZIP 中。</p>
    <el-table :data="result.resources.slice(-12).reverse()" max-height="260">
      <el-table-column label="时间" min-width="170"><template #default="{row}">{{ new Date(row.at).toLocaleString() }}</template></el-table-column>
      <el-table-column label="S2L 堆 / RSS" min-width="170"><template #default="{row}">{{ mib(row.resources.heap_bytes) }} / {{ mib(row.resources.rss_bytes) }}</template></el-table-column>
      <el-table-column label="任务 / FD"><template #default="{row}">{{ row.resources.goroutines }} / {{ row.resources.open_fds ?? '—' }}</template></el-table-column>
      <el-table-column label="磁盘可用" min-width="130"><template #default="{row}">{{ row.resources.disk_sampled ? mib(row.resources.disk_free_bytes || 0) : '—' }}</template></el-table-column>
    </el-table>
  </template>
</template>

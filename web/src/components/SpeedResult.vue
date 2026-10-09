<script setup>
defineProps({ result: Object });
const transfer = (result, key) =>
  result[key] ||
  (key === "download"
    ? result
    : { ok: false, error: result.error || "未返回上传结果" });
</script>
<template>
  <div v-if="result" class="speed-lines">
    <div v-for="key in ['download', 'upload']" :key="key">
      <span class="muted">{{ key === "download" ? "↓ 下载" : "↑ 上传" }} </span
      ><strong :class="transfer(result, key).ok ? 'speed-ok' : 'error-text'">{{
        transfer(result, key).ok
          ? Number(transfer(result, key).speed_mbps || 0).toFixed(1) + " Mbps"
          : "失败"
      }}</strong>
      <div v-if="transfer(result, key).error" class="error-text">
        {{ transfer(result, key).error }}
      </div>
    </div>
    <small class="muted">连接延迟 {{ result.latency_ms ?? "—" }} ms</small>
  </div>
  <span v-else class="muted">尚未测速</span>
</template>

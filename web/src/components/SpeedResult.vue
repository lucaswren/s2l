<script setup>
import { tr } from '../i18n/index.js';

defineProps({ result: Object });
</script>
<template>
  <div v-if="result" class="speed-lines">
    <div v-for="key in ['download', 'upload']" :key="key">
      <span class="muted">{{ key === "download" ? tr("↓ 下载") : tr("↑ 上传") }} </span
      ><strong :class="result[key].ok ? 'speed-ok' : 'error-text'">{{
        result[key].ok
          ? Number(result[key].speed_mbps || 0).toFixed(1) + " Mbps"
          : tr("失败")
      }}</strong>
      <div v-if="result[key].error" class="error-text">
        {{ result[key].error }}
      </div>
    </div>
    <small class="muted">{{ tr("连接延迟") }} {{ result.latency_ms ?? "—" }} ms</small>
  </div>
  <span v-else class="muted">{{ tr("尚未测速") }}</span>
</template>

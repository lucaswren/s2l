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
    <small v-if="result.egress" class="muted">
      {{ result.egress === 'wan'
        ? tr("服务器经本机 WAN（{0}）", result.wan_interface)
        : tr("服务器经 SOCKS5（{0}）", result.name || result.addr) }}
      · {{ tr("不经过 L2TP 客户端") }}
    </small>
    <div v-if="result.download_url && result.upload_url" class="muted">
      <small>{{ tr("测速目标") }} {{ result.download_url }} · {{ result.upload_url }}</small>
    </div>
    <div v-if="result.sample_bytes" class="muted">
      <small>{{ tr("每方向样本 {0} MB", result.sample_bytes / 1_000_000) }}</small>
    </div>
    <small class="muted">{{ tr("连接延迟") }} {{ result.latency_ms ?? "—" }} ms</small>
  </div>
  <span v-else class="muted">{{ tr("尚未测速") }}</span>
</template>

<script setup>
import { dateTime, tr } from "../i18n/index.js";

defineProps({ process: Object });
</script>

<template>
  <div v-if="process" class="muted">
    <p>{{ tr("计划重启") }} {{ process.restarts ?? "—" }} · {{ tr("异常退出") }} {{ process.exits ?? "—" }}</p>
    <p v-if="process.last_unexpected_exit">
      {{ tr("最近异常退出") }} {{ dateTime(process.last_unexpected_exit_at) }} · {{ process.last_unexpected_exit }}
    </p>
  </div>
</template>

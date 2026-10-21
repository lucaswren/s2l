<script setup>
import { tr } from '../i18n/index.js';

defineProps({ mapping: Object, actionId: String });
defineEmits(["edit", "action", "remove"]);
</script>
<template>
  <div class="actions">
    <el-button :disabled="!!actionId" @click="$emit('edit', mapping)">{{ tr("编辑") }}</el-button
    ><el-button
      v-if="mapping.status !== 'running'"
      type="success"
      :loading="actionId === mapping.id + 'start'"
      :disabled="!!actionId"
      @click="$emit('action', mapping.id, 'start')"
      >{{ tr("启动") }}</el-button
    ><el-button
      v-if="mapping.status !== 'stopped'"
      type="warning"
      :loading="actionId === mapping.id + 'stop'"
      :disabled="!!actionId"
      @click="$emit('action', mapping.id, 'stop')"
      >{{ mapping.status === 'error' ? tr("清理并停止") : tr("停止") }}</el-button
    ><el-button
      type="danger"
      plain
      :disabled="!!actionId"
      @click="$emit('remove', mapping.id)"
      >{{ tr("删除") }}</el-button
    >
  </div>
</template>

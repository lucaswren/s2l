<script setup>
import { tr } from '../i18n/index.js';

defineProps({ node: Object, testingId: String });
defineEmits(["speed", "network", "edit", "remove"]);
</script>
<template>
  <div class="actions">
    <el-button @click="$emit('network', node)">{{ tr("网络测试") }}</el-button>
    <el-button
      type="primary"
      plain
      :loading="testingId === node.id"
      :disabled="!!testingId && testingId !== node.id"
      @click="$emit('speed', node)"
      >{{ tr("上下行测速") }}</el-button
    ><el-button @click="$emit('edit', node)">{{ tr("编辑") }}</el-button
    ><el-button type="danger" plain @click="$emit('remove', node.id)"
      >{{ tr("删除") }}</el-button
    >
  </div>
</template>

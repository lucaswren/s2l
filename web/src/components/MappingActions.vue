<script setup>
defineProps({ mapping: Object, actionId: String });
defineEmits(["edit", "action", "remove"]);
</script>
<template>
  <div class="actions">
    <el-button @click="$emit('edit', mapping)">编辑</el-button
    ><el-button
      v-if="mapping.status !== 'running'"
      type="success"
      :loading="actionId === mapping.id + 'start'"
      :disabled="!!actionId"
      @click="$emit('action', mapping.id, 'start')"
      >启动</el-button
    ><el-button
      v-else
      type="warning"
      :loading="actionId === mapping.id + 'stop'"
      :disabled="!!actionId"
      @click="$emit('action', mapping.id, 'stop')"
      >停止</el-button
    ><el-button
      type="danger"
      plain
      :disabled="!!actionId"
      @click="$emit('remove', mapping.id)"
      >删除</el-button
    >
  </div>
</template>

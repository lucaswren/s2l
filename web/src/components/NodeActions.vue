<script setup>
defineProps({ node: Object, testingId: String });
defineEmits(["speed", "network", "edit", "remove"]);
</script>
<template>
  <div class="actions">
    <el-button @click="$emit('network', node)">网络测试</el-button>
    <el-button
      type="primary"
      plain
      :loading="testingId === node.id"
      :disabled="!!testingId && testingId !== node.id"
      @click="$emit('speed', node)"
      >上下行测速</el-button
    ><el-button @click="$emit('edit', node)">编辑</el-button
    ><el-button type="danger" plain @click="$emit('remove', node.id)"
      >删除</el-button
    >
  </div>
</template>

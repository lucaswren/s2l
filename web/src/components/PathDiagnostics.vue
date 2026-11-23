<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { api } from "../api/client";
import { tr } from "../i18n/index.js";
import MappingDiagnosticResult from "./MappingDiagnosticResult.vue";
import SpeedResult from "./SpeedResult.vue";

const props = defineProps({
  nodes: { type: Array, required: true },
  mappings: { type: Array, required: true },
  mapping: Object,
  launch: Object,
});
const emit = defineEmits(["speed-result", "launched", "mapping-selected"]);
const kind = ref(props.launch?.kind ?? "speed");
const nodeID = ref(props.launch?.kind === "speed" ? props.launch.id : "");
const mappingID = ref(props.launch?.kind === "mapping" ? props.launch.id : "");
const running = computed(() => props.mappings.filter(m => m.status === "running"));
const mappingNode = computed(() => props.nodes.find(node => node.id === running.value.find(m => m.id === mappingID.value)?.node_id));
const id = computed(() => kind.value === "speed" ? nodeID.value : mappingID.value);
const canRun = computed(() => !!id.value && (kind.value === "speed" || running.value.some(mapping => mapping.id === id.value)));
const result = ref(null);
const error = ref("");
const busy = ref(false);
let controller;

watch([() => props.nodes, () => props.mapping?.node_id], ([nodes, preferred]) => {
  if (!nodes.some(n => n.id === nodeID.value)) nodeID.value = nodes.find(n => n.id === preferred)?.id ?? nodes[0]?.id ?? "";
}, { immediate: true });
watch(() => props.mappings, maps => {
  if (!maps.some(m => m.id === mappingID.value)) mappingID.value = running.value.find(m => m.id === props.mapping?.id)?.id ?? running.value[0]?.id ?? "";
}, { immediate: true });

function cancel() {
  controller?.abort();
  controller = undefined;
  busy.value = false;
}
watch([kind, id], () => {
  cancel();
  result.value = null;
  error.value = "";
});
onBeforeUnmount(cancel);

async function run() {
  if (busy.value || !canRun.value) return;
  const selected = id.value;
  const isSpeed = kind.value === "speed";
  const pending = new AbortController();
  controller = pending;
  busy.value = true;
  result.value = null;
  error.value = "";
  try {
    const response = await (isSpeed
      ? api.speedTestNode(selected, undefined, pending.signal)
      : api.mappingDiagnostic(selected, pending.signal));
    if (controller === pending) {
      result.value = response;
      if (isSpeed) emit("speed-result", selected, response);
    }
  } catch (e) {
    if (controller === pending && e.name !== "AbortError") error.value = e.message;
  } finally {
    if (controller === pending) cancel();
  }
}
onMounted(() => {
  if (!props.launch) return;
  if (id.value === props.launch.id) run();
  emit("launched");
});
</script>

<template>
  <div class="path-diagnostics">
    <el-form label-position="top" @submit.prevent="run">
      <el-form-item :label='tr("检测类型")'>
        <el-select v-model="kind" :aria-label='tr("检测类型")'>
          <el-option :label='tr("上下行测速")' value="speed" />
          <el-option :label='tr("映射转发实测")' value="mapping" />
        </el-select>
      </el-form-item>
      <el-form-item v-if="kind === 'speed'" :label='tr("出口节点")'>
        <el-select v-model="nodeID" :aria-label='tr("出口节点")'>
          <el-option v-for="node in nodes" :key="node.id" :label="node.name" :value="node.id" />
        </el-select>
      </el-form-item>
      <el-form-item v-else :label='tr("接入映射")'>
        <el-select v-model="mappingID" @change="emit('mapping-selected', $event)" :aria-label='tr("接入映射")'>
          <el-option v-for="entry in mappings" :key="entry.id" :label="entry.tun_name" :value="entry.id" :disabled="entry.status !== 'running'" />
        </el-select>
      </el-form-item>
      <p class="muted">{{ kind === "speed"
        ? tr("服务器经所选出口向 Cloudflare 测速。")
        : tr("服务器模拟 PPP 入口，经映射策略与 TUN 转发。") }}</p>
      <p v-if="kind === 'mapping' && id && !canRun" class="muted">{{ tr("映射未运行，请先启动") }}</p>
      <el-button type="primary" native-type="submit" :loading="busy" :disabled="!canRun">{{ tr("开始测试") }}</el-button>
      <el-button v-if="busy" @click="cancel">{{ tr("停止检测") }}</el-button>
    </el-form>
    <el-alert v-if="error" class="section-gap" type="error" :title="error" :closable="false" />
    <div v-if="result" class="section-gap" role="status">
      <SpeedResult v-if="kind === 'speed'" :result="result" />
      <MappingDiagnosticResult v-else :result="result" :node="mappingNode" />
    </div>
  </div>
</template>

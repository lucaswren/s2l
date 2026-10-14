<script setup>
import { computed, ref, watch } from "vue";
import { api } from "../api/client";
const props = defineProps({ mapping: Object });
const emit = defineEmits(["close"]);
const url = ref("");
const result = ref(null);
const error = ref("");
const busy = ref(false);
watch(() => props.mapping?.id, () => { url.value = ""; result.value = null; error.value = ""; });
const stages = computed(() => result.value ? Object.entries({dns:"DNS 解析",socks:"SOCKS5 连接",tls:"TLS 握手",http:"HTTP 响应",download:"下载读取"}).map(([key,label]) => ({label,...result.value[key]})) : []);
const labels = {healthy:"成功",failed:"失败",skipped:"未执行",limited:"已达到读取上限"};
async function run() {
  busy.value = true; result.value = null; error.value = "";
  try { result.value = await api.diagnoseMapping(props.mapping.id, url.value.trim()); }
  catch (e) { error.value = e.message; }
  finally { busy.value = false; }
}
</script>
<template>
  <el-dialog :model-value="!!mapping" title="图片目标诊断" width="680px" class="edit-dialog" :close-on-click-modal="false" :show-close="!busy" :close-on-press-escape="!busy" @close="emit('close')">
    <p class="muted">通过当前映射的解析器和 SOCKS5 检查目标。与实际客户端结果对比，可区分上游与 L2TP 路径故障。仅支持公网 IPv4 HTTP/HTTPS。</p>
    <el-input v-model="url" :disabled="busy" placeholder="https://图片服务器/图片路径" aria-label="图片诊断网址" @keyup.enter="!busy && url.trim() && run()" />
    <p class="muted">不会携带应用登录凭据或跟随跳转；最多读取约 2 MiB。需要应用认证的图片可能返回 403，此结果不等同于网络故障。</p>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <template v-if="result">
      <p>解析地址：{{ result.addresses.join('、') || '无' }}</p>
      <p>HTTP：{{ result.status_code || '无响应' }} · {{ result.content_type || '未知类型' }} · 读取 {{ result.bytes }} 字节</p>
      <el-table :data="stages"><el-table-column prop="label" label="阶段" /><el-table-column label="结果"><template #default="{row}">{{ labels[row.state] || row.state }}</template></el-table-column><el-table-column prop="duration_ms" label="耗时 ms" /><el-table-column prop="error" label="说明" min-width="220" /></el-table>
    </template>
    <template #footer><el-button :disabled="busy" @click="emit('close')">关闭</el-button><el-button type="primary" :loading="busy" :disabled="!url.trim()" @click="run">开始诊断</el-button></template>
  </el-dialog>
</template>

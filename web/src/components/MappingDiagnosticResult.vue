<script setup>
import { tr } from "../i18n/index.js";
import { healthLabels } from "../utils/health";

defineProps({ result: { type: Object, required: true }, node: Object });
</script>

<template>
  <el-tag :type="result.ok ? 'success' : 'warning'">{{ result.ok ? tr("映射转发正常") : tr("检测完成，请查看分项") }}</el-tag>
  <p v-if="node">{{ tr("出口节点") }} {{ node.name }} · {{ node.egress === "wan" ? `${tr("本机 WAN")} ${node.wan_interface}` : "SOCKS5" }}</p>
  <p>{{ tr("规则检查：") }}{{ healthLabels[result.rules?.state] ?? "—" }}</p>
  <p>{{ tr("HTTPS 转发：") }}{{ healthLabels[result.forwarding?.state] ?? "—" }} · {{ result.forwarding?.duration_ms ?? "—" }} ms</p>
  <p v-if="result.forwarding?.exit_ip">{{ tr("映射出口 IP：") }}{{ result.forwarding.exit_ip }}</p>
  <p v-if="result.node?.exit_ip">{{ tr("节点出口 IP：") }}{{ result.node.exit_ip }}</p>
  <p v-if="result.exit_comparison === 'different'">{{ tr("两次出口观测不同；上游可能采用动态出口，请结合节点配置核对。") }}</p>
  <p v-if="result.node?.error">{{ result.node.error }}</p>
  <p v-if="result.forwarding?.error">{{ result.forwarding.error }}</p>
  <p class="muted">{{ tr("服务器模拟 PPP 入口，经映射策略与 TUN 转发。") }}</p>
</template>

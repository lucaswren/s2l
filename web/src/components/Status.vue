<script setup>
import { computed } from "vue";

const props = defineProps({
  status: {
    type: Object,
    default: () => ({ tun_traffic: [], singbox_pids: {} }),
  },
});

const pids = computed(() =>
  Object.entries(props.status.singbox_pids || {}).map(([tun, pid]) => ({
    tun,
    pid,
  })),
);

const health = computed(() => Object.entries(props.status.upstream_health || {}).map(([id, h]) => ({id, ...h})));
const healthLabels = {checking: "检测中", healthy: "正常", unstable: "连接不稳定", unavailable: "上游不可用", recovering: "恢复中", degraded: "部分可用", unsupported: "不支持"};
function protocolLabel(result) {
 if (result?.state === "degraded" && result?.method === "tcp-fallback") return "仅备用可用";
 return healthLabels[result?.state] || "检测中";
}
function protocolDetail(result) {
 const methods = {https: "HTTPS 转发", "dns-udp": "UDP 中继实测", "udp-associate": "UDP 协商", doh: "DoH", "tcp-fallback": "TCP DNS 备用", "doh/tcp": "DoH / TCP DNS"};
 return [methods[result?.method], result?.error].filter(Boolean).join(" · ") || "等待检测";
}
function formatBytes(n) {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = Number(n);
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`;
}
</script>
<template>
  <div class="section-head">
    <div>
      <h2>运行监控</h2>
      <p class="muted">上游 TCP、UDP、DNS 健康状态、sing-box 进程与 TUN 累计流量</p>
    </div>
  </div>
  <el-card shadow="never" class="section-gap">
    <template #header>上游健康状态</template>
    <el-table :data="health" empty-text="暂无活动映射">
      <el-table-column prop="tun_name" label="接口" />
      <el-table-column label="链路状态"><template #default="{ row }">{{ healthLabels[row.state] || row.state }}</template></el-table-column>
      <el-table-column v-for="protocol in ['tcp', 'udp', 'dns']" :key="protocol" :label="protocol.toUpperCase()" min-width="100">
        <template #default="{ row }"><el-tooltip :content="protocolDetail(row[protocol])"><span>{{ protocolLabel(row[protocol]) }}</span></el-tooltip></template>
      </el-table-column>
      <el-table-column label="最近检测"><template #default="{ row }">{{ row.checked_at && !row.checked_at.startsWith('0001') ? new Date(row.checked_at).toLocaleString() : '等待检测' }}</template></el-table-column>
      <el-table-column prop="error" label="故障说明" min-width="220" />
    </el-table>
    <p class="muted">映射运行中不代表上游可用。UDP 不支持时 TCP 仍可正常使用；DNS 备用可用不表示已自动切换解析器。探测不验证客户端完整链路或所有 UDP 应用。</p>
  </el-card>
  <el-row :gutter="16"
    ><el-col :xs="24" :md="8"
      ><el-card shadow="never" class="section-gap"
        ><template #header>运行进程</template
        ><el-table :data="pids" empty-text="无运行实例"
          ><el-table-column prop="tun" label="接口" /><el-table-column
            prop="pid"
            label="PID" /></el-table></el-card></el-col
    ><el-col :xs="24" :md="16"
      ><el-card shadow="never"
        ><template #header>接口流量</template
        ><el-table :data="status.tun_traffic" empty-text="暂无流量"
          ><el-table-column
            prop="tun_name"
            label="接口"
            min-width="100"
          /><el-table-column label="接收" min-width="100"
            ><template #default="{ row }">{{
              formatBytes(row.rx_bytes)
            }}</template></el-table-column
          ><el-table-column label="发送" min-width="100"
            ><template #default="{ row }">{{
              formatBytes(row.tx_bytes)
            }}</template></el-table-column
          ></el-table
        ></el-card
      ></el-col
    ></el-row
  >
</template>

<script setup>
import { computed, ref } from "vue";
import { api } from "../api/client";
const emit = defineEmits(["changed", "toast"]);
const retrying = ref("");
async function retry(id) {
  retrying.value = id;
  try { await api.mappingAction(id, "retry-recovery"); emit("changed"); }
  catch(e) { emit("toast", {type:"err",message:e.message}); }
  finally { retrying.value = ""; }
}
function date(value) { return value && !value.startsWith('0001') ? new Date(value).toLocaleString() : '—'; }

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
const healthLabels = {checking: "检测中", healthy: "正常", unstable: "连接不稳定", unavailable: "不可用", recovering: "恢复中", 'circuit-open': "恢复熔断", degraded: "部分可用", unsupported: "不支持"};
function protocolLabel(result) {
 if (result?.state === "degraded" && result?.method === "tcp-fallback") return "仅备用可用";
 return healthLabels[result?.state] || "检测中";
}
function protocolDetail(result) {
 const methods = {https: "HTTPS 转发", "dns-udp": "UDP 中继实测", "udp-associate": "UDP 协商", doh: "DoH", tcp: "TCP DNS", "tcp-fallback": "TCP DNS 备用", "doh/tcp": "DoH / TCP DNS"};
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
      <p class="muted">上游协议探测、本地恢复、实时 DNS 解析器与接口丢包</p>
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
      <el-table-column label="本地链路" min-width="100"><template #default="{row}"><el-tooltip :content="row.recovery_error || '独立检查进程和 TUN，不受上游探测退避影响'"><span>{{ healthLabels[row.local_state] || '检测中' }}</span></el-tooltip></template></el-table-column>
      <el-table-column label="实际 DNS" min-width="145"><template #default="{row}"><div>{{ row.resolver?.active === 'tcp' ? 'TCP DNS' : row.resolver?.active === 'doh' ? 'DoH' : '等待启动' }}</div><div class="muted">备用成功 {{ row.resolver?.fallbacks || 0 }} · 失败 {{ row.resolver?.failures || 0 }}</div></template></el-table-column>
      <el-table-column label="连接错误记录" min-width="180"><template #default="{row}"><el-tooltip :content="'当前进程日志分类计数，不等同于失败请求数量；网络不可达 ' + (row.process?.network_errors || 0) + '，本次服务累计退出 ' + (row.process?.exits || 0)"><span>超时 {{ row.process?.timeouts || 0 }} · 拒绝 {{ row.process?.refused || 0 }} · TLS {{ row.process?.tls_failures || 0 }} · 代理拒绝 {{ row.process?.proxy_rejections || 0 }}</span></el-tooltip></template></el-table-column>
      <el-table-column label="最近检测"><template #default="{ row }">{{ row.checked_at && !row.checked_at.startsWith('0001') ? new Date(row.checked_at).toLocaleString() : '等待检测' }}</template></el-table-column>
      <el-table-column prop="error" label="故障说明" min-width="220" />
      <el-table-column label="自动恢复" min-width="230"><template #default="{row}">
        <div>{{ {closed:'正常重试',open:'冷却中','half-open':'试探观察中'}[row.recovery?.circuit] || '待检查' }} · 尝试 {{ row.recovery?.attempts || 0 }} · 失败 {{ row.recovery?.failures || 0 }}</div>
        <div class="muted">下次重试 {{ date(row.recovery?.next_retry) }}</div>
        <el-button v-if="row.local_state !== 'healthy'" size="small" :disabled="!!retrying" :loading="retrying === row.id" @click="retry(row.id)">手动重试恢复</el-button>
      </template></el-table-column>
    </el-table>
    <p class="muted">映射运行中不代表上游可用。UDP 不支持时 TCP 仍可正常使用。DNS 列是独立上游探测，“实际 DNS”显示映射当前解析器；自动模式按真实查询失败切换。探测不验证客户端完整链路或所有 UDP 应用。</p>
    <p class="muted">本地恢复预算为 10 分钟内 3 次，耗尽后冷却 5 分钟并试探一次；连续稳定 2 分钟后重置预算。单纯上游不可用不会重启本地隧道。手动重试会重置恢复预算。</p>
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
          ><el-table-column label="丢包 / 错误" min-width="160"><template #default="{row}">{{ (row.rx_dropped || 0) + (row.tx_dropped || 0) }} / {{ (row.rx_errors || 0) + (row.tx_errors || 0) }}<div v-if="row.error" class="error-text">{{ row.error }}</div></template></el-table-column></el-table
        ></el-card
      ></el-col
    ></el-row
  >
</template>

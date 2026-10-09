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
      <p class="muted">sing-box 进程与 TUN 累计流量</p>
    </div>
  </div>
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

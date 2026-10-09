<script setup>
import {
  computed,
  defineAsyncComponent,
  onMounted,
  onUnmounted,
  ref,
} from "vue";
import { api } from "./api/client";
const Guide = defineAsyncComponent(() => import("./components/Guide.vue"));
import Node from "./components/Node.vue";
import Mapping from "./components/Mapping.vue";
const Settings = defineAsyncComponent(
  () => import("./components/Settings.vue"),
);
import Status from "./components/Status.vue";
import { ElMessage } from "element-plus";

const nodes = ref([]);
const mappings = ref([]);
const status = ref({ uptime_sec: 0, tun_traffic: [], singbox_pids: {} });
const error = ref("");
const activeTab = ref("lines");
const helpVisible = ref(false);
const q = ref("");
let timer;
const refreshing = ref(false);

const runningCount = computed(
  () => mappings.value.filter((m) => m.status === "running").length,
);
const errCount = computed(
  () => mappings.value.filter((m) => m.status === "error").length,
);

const filteredNodes = computed(() => {
  const s = q.value.trim().toLowerCase();
  if (!s) return nodes.value;
  return nodes.value.filter((n) =>
    [n.name, n.addr, n.id].join(" ").toLowerCase().includes(s),
  );
});

const filteredMappings = computed(() => {
  const s = q.value.trim().toLowerCase();
  if (!s) return mappings.value;
  const nodeMap = Object.fromEntries(nodes.value.map((n) => [n.id, n.name]));
  return mappings.value.filter((m) =>
    [
      m.tun_name,
      String(m.table_id),
      m.subnet,
      m.l2tp_user,
      m.status,
      nodeMap[m.node_id],
      m.id,
    ]
      .join(" ")
      .toLowerCase()
      .includes(s),
  );
});

function pushToast({ type = "info", message }) {
  ElMessage({
    type: type === "ok" ? "success" : type === "err" ? "error" : "info",
    message,
    duration: 5000,
    showClose: true,
  });
}

async function refresh() {
  if (refreshing.value) return;
  refreshing.value = true;
  try {
    const [n, m, st] = await Promise.all([
      api.listNodes(),
      api.listMappings(),
      api.status(),
    ]);
    nodes.value = Array.isArray(n) ? n : [];
    mappings.value = Array.isArray(m) ? m : [];
    status.value = {
      uptime_sec: st?.uptime_sec || 0,
      tun_traffic: Array.isArray(st?.tun_traffic) ? st.tun_traffic : [],
      singbox_pids: st?.singbox_pids || {},
    };
    error.value = "";
  } catch (e) {
    error.value = e.message;
  } finally {
    refreshing.value = false;
  }
}

function formatUptime(sec) {
  sec = Number(sec) || 0;
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

onMounted(() => {
  refresh();
  timer = setInterval(refresh, 5000);
});
onUnmounted(() => clearInterval(timer));
</script>
<template>
  <el-config-provider size="default">
    <div class="page">
      <header class="hero">
        <div>
          <span class="brand">s2l / 线路管理</span>
          <h1>SOCKS5 → L2TP</h1>
          <p class="muted">SOCKS5 出口与 L2TP 接入管理</p>
        </div>
        <div class="toolbar">
          <el-input
            v-if="activeTab === 'lines'"
            v-model="q"
            clearable
            placeholder="搜索节点、接口或账号"
            aria-label="搜索节点或映射"
          />
          <div class="toolbar-buttons">
            <el-button
              v-if="activeTab === 'lines'"
              :loading="refreshing"
              @click="refresh"
              >刷新</el-button
            >
            <el-button @click="helpVisible = true">帮助</el-button>
          </div>
        </div>
      </header>
      <el-row :gutter="12" class="stats">
        <el-col :xs="12" :sm="6"
          ><el-card shadow="never"
            ><el-statistic title="节点" :value="nodes.length" /></el-card
        ></el-col>
        <el-col :xs="12" :sm="6"
          ><el-card shadow="never"
            ><el-statistic title="映射" :value="mappings.length" /></el-card
        ></el-col>
        <el-col :xs="12" :sm="6"
          ><el-card shadow="never"
            ><el-statistic
              title="运行中"
              :value="runningCount"
              :value-style="{ color: 'var(--el-color-success)' }" /></el-card
        ></el-col>
        <el-col :xs="12" :sm="6"
          ><el-card shadow="never"
            ><el-statistic
              title="异常"
              :value="errCount"
              :value-style="{
                color: errCount ? 'var(--el-color-danger)' : '',
              }" /></el-card
        ></el-col>
      </el-row>
      <el-alert
        v-if="error"
        :title="error"
        type="error"
        show-icon
        :closable="false"
        class="section-gap"
      />
      <el-card shadow="never" class="workspace">
        <el-tabs v-model="activeTab">
          <el-tab-pane label="线路管理" name="lines">
            <Mapping
              :nodes="nodes"
              :mappings="filteredMappings"
              :all-mappings="mappings"
              @changed="refresh"
              @toast="pushToast"
            />
            <el-divider />
            <Node
              :nodes="filteredNodes"
              @changed="refresh"
              @toast="pushToast"
            />
            <el-collapse class="monitor-collapse"
              ><el-collapse-item title="运行监控" name="monitor"
                ><Status :status="status" /></el-collapse-item
            ></el-collapse>
          </el-tab-pane>
          <el-tab-pane label="系统设置" name="settings" lazy
            ><Settings @toast="pushToast"
          /></el-tab-pane>
        </el-tabs>
      </el-card>
      <el-drawer v-model="helpVisible" title="使用说明" size="min(540px, 100vw)"
        ><Guide
      /></el-drawer>
      <footer class="muted">
        运行 {{ formatUptime(status.uptime_sec) }} · 状态每 5 秒刷新
      </footer>
    </div>
  </el-config-provider>
</template>

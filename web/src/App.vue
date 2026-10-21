<script setup>
import { tr } from './i18n/index.js';
import LanguageSelector from './components/LanguageSelector.vue';
defineProps({authEnabled:{type:Boolean,default:false}});

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
const Status=defineAsyncComponent(()=>import("./components/Status.vue"));
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
let refreshPending = false;
let disposed = false;

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
    [n.name, n.addr, n.id,n.group].join(" ").toLowerCase().includes(s),
  );
});

const filteredMappings = computed(() => {
  const s = q.value.trim().toLowerCase();
  if (!s) return mappings.value;
  const nodeMap = Object.fromEntries(nodes.value.map((n) => [n.id, n.name]));
  return mappings.value.filter((m) =>
    [
      m.tun_name,m.group,
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
    message: tr(message),
    duration: 5000,
    showClose: true,
  });
}

async function refresh() {
  if (refreshing.value) {
    refreshPending = true;
    return;
  }
  if (disposed) return;
  refreshing.value = true;
  try {
    const [n, m, st] = await Promise.all([
      api.listNodes(),
      api.listMappings(),
      api.status(),
    ]);
    if (disposed) return;
    nodes.value = Array.isArray(n) ? n : [];
    mappings.value = Array.isArray(m) ? m : [];
    status.value = {
      uptime_sec: st?.uptime_sec || 0,
      tun_traffic: Array.isArray(st?.tun_traffic) ? st.tun_traffic : [],
      singbox_pids: st?.singbox_pids || {},
      upstream_health: st?.upstream_health || {},
	  mappings: Array.isArray(st?.mappings) ? st.mappings : (Array.isArray(m) ? m : []),
	  sessions: Array.isArray(st?.sessions) ? st.sessions : [],
	  sessions_error: st?.sessions_error || '',
	  sessions_checked_at: st?.sessions_checked_at || '',
      configuration_recovery: st?.configuration_recovery || '',
    };
    error.value = "";
  } catch (e) {
    error.value = e.message;
  } finally {
    refreshing.value = false;
    if (refreshPending && !disposed) {
      refreshPending = false;
      void refresh();
    }
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
onUnmounted(() => { disposed = true; clearInterval(timer); });
</script>
<template>
  <el-config-provider size="default">
    <div class="page">
      <header class="hero" :class="{compact:activeTab==='settings'}">
        <div>
          <h1>S2L</h1><p class="muted">SOCKS5 → L2TP</p>
        </div>
        <div class="toolbar">
          <el-input
            v-if="activeTab === 'lines'"
            v-model="q"
            clearable
            :placeholder='tr("搜索节点、接口或账号")'
            :aria-label='tr("搜索节点或映射")'
          />
          <div class="toolbar-buttons">
            <LanguageSelector persist />
            <el-button @click="helpVisible = true">{{ tr("帮助") }}</el-button>
          </div>
        </div>
      </header>
      <el-row v-if="activeTab==='lines'" :gutter="16" class="stats">
        <el-col :xs="12" :sm="6"
          ><el-card shadow="never"
            ><el-statistic :title='tr("节点")' :value="nodes.length" /></el-card
        ></el-col>
        <el-col :xs="12" :sm="6"
          ><el-card shadow="never"
            ><el-statistic :title='tr("映射")' :value="mappings.length" /></el-card
        ></el-col>
        <el-col :xs="12" :sm="6"
          ><el-card shadow="never"
            ><el-statistic
              :title='tr("运行中")'
              :value="runningCount"
              :value-style="{ color: 'var(--el-color-success)' }" /></el-card
        ></el-col>
        <el-col :xs="12" :sm="6"
          ><el-card shadow="never"
            ><el-statistic
              :title='tr("异常")'
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
      <el-alert v-if="status.configuration_recovery" :title="status.configuration_recovery" type="warning" :closable="false" class="section-gap" />
      <section class="workspace">
        <el-tabs v-model="activeTab">
          <el-tab-pane :label='tr("线路管理")' name="lines">
            <Mapping
              :nodes="nodes"
              :mappings="filteredMappings"
              :all-mappings="mappings"
              :health="status.upstream_health"
              @changed="refresh"
              @toast="pushToast"
            />
            <Node
              :nodes="filteredNodes"
              @changed="refresh"
              @toast="pushToast"
            />
            <el-collapse class="monitor-collapse"
              ><el-collapse-item :title='tr("运行监控")' name="monitor"
                ><Status :status="status" @changed="refresh" @toast="pushToast" /></el-collapse-item
            ></el-collapse>
          </el-tab-pane>
          <el-tab-pane :label='tr("系统设置")' name="settings" lazy
            ><Settings :auth-enabled="authEnabled" :active="activeTab === 'settings'" @toast="pushToast" @changed="refresh"
          /></el-tab-pane>
        </el-tabs>
      </section>
      <el-drawer v-model="helpVisible" :title='tr("使用说明")' size="min(540px, 100vw)" destroy-on-close
        ><Guide :mappings="mappings" :health="status.upstream_health"
      /></el-drawer>
      <footer class="muted"> {{ tr("运行") }} {{ formatUptime(status.uptime_sec) }}
      </footer>
    </div>
  </el-config-provider>
</template>

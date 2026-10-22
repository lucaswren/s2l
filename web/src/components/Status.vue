<script setup>
import { tr, dateTime } from '../i18n/index.js';

import { computed, ref, defineAsyncComponent, onBeforeUnmount } from "vue";
import { api } from "../api/client";
import {healthLabels,protocolSummary} from "../utils/health";
const MonitoringDetails=defineAsyncComponent(()=>import("./MonitoringDetails.vue"));
const diagnostic=ref(null),diagnosing=ref(""),diagnosticError=ref("");
let diagnosticController,diagnosticVersion=0;
function closeDiagnostic(){diagnosticVersion++;diagnosticController?.abort();diagnosing.value="";diagnostic.value=null;diagnosticError.value="";}
onBeforeUnmount(closeDiagnostic);
async function diagnose(id){
 closeDiagnostic();const version=diagnosticVersion;diagnosing.value=id;diagnosticController=new AbortController();
 try{const result=await api.mappingDiagnostic(id,diagnosticController.signal);if(version===diagnosticVersion)diagnostic.value=result;}
 catch(e){if(e.name!=="AbortError"&&version===diagnosticVersion)diagnosticError.value=e.message;}
 finally{if(version===diagnosticVersion)diagnosing.value="";}
}
const emit = defineEmits(["changed", "toast"]);
const retrying = ref("");
const tableView = ref(false);
async function retry(id) {
  if (retrying.value) return;
  retrying.value = id;
  try { await api.mappingAction(id, "retry-recovery"); emit("changed"); }
  catch(e) { emit("toast", {type:"err",message:e.message}); }
  finally { retrying.value = ""; }
}
function date(value) { return value && !value.startsWith('0001') ? dateTime(value) : '—'; }

const props = defineProps({
 active:{type:Boolean,default:true},
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
function protocolLabel(result) {
 if (result?.method === "udp-associate" && result?.error?.includes("code=")) return tr("节点拒绝 UDP");
 if (result?.state === "degraded" && result?.method === "tcp-fallback") return tr("仅备用可用");
 return healthLabels[result?.state] || tr("检测中");
}
function protocolDetail(result) {
 if(result?.state==='blocked')return tr("未发起 UDP 探测");
 const methods = {get https() { return tr("HTTPS 转发"); }, get "dns-udp"() { return tr("UDP 中继实测"); }, get "udp-associate"() { return tr("UDP 协商"); }, doh: "DoH", tcp: "TCP DNS", get "tcp-fallback"() { return tr("TCP DNS 备用"); }, "doh/tcp": "DoH / TCP DNS"};
 return [methods[result?.method],protocolSummary(result), result?.error, result?.checked_at && !result.checked_at.startsWith('0001') ? tr("检测于 {0}", dateTime(result.checked_at)) : ""].filter(Boolean).join(" · ") || tr("等待检测");
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
 <section class="runtime-monitor" :class="{'show-tables':tableView}">
  <div class="mobile-table-toggle"><el-switch v-model="tableView" :active-text="tr('表格视图')" /></div>
  <p v-if="status.collected_at" class="muted">{{tr('采集于')}} {{date(status.collected_at)}}</p>
  <el-card shadow="never" class="section-gap">
    <template #header>{{ tr("映射健康状态") }}</template>
    <el-table scrollbar-always-on class="desktop-list" :data="health" :empty-text='tr("暂无活动映射")'>
      <el-table-column min-width="130" prop="tun_name" :label='tr("接口")' />
      <el-table-column min-width="130" :label='tr("链路状态")'><template #default="{ row }">{{ healthLabels[row.state] || row.state }}</template></el-table-column>
      <el-table-column v-for="protocol in ['tcp', 'udp', 'dns']" :key="protocol" :label="protocol.toUpperCase()" min-width="100">
        <template #default="{ row }"><el-tooltip :content="protocolDetail(row[protocol])"><span>{{ protocolLabel(row[protocol]) }}</span></el-tooltip></template>
      </el-table-column>
      <el-table-column :label='tr("本地链路")' min-width="100"><template #default="{row}"><el-tooltip :content="row.recovery_error || tr(&quot;独立检查进程和 TUN，不受上游探测退避影响&quot;)"><span>{{ healthLabels[row.local_state] || tr("检测中") }}</span></el-tooltip></template></el-table-column>
      <el-table-column :label='tr("实际 DNS")' min-width="145"><template #default="{row}"><div>{{ row.resolver?.active === 'tcp' ? 'TCP DNS' : row.resolver?.active === 'doh' ? 'DoH':tr("等待启动") }}</div><div class="muted">{{ tr("备用成功") }} {{ row.resolver?.fallbacks || 0 }} {{ tr("· 失败") }} {{ row.resolver?.failures || 0 }}</div></template></el-table-column>
      <el-table-column :label='tr("连接错误记录")' min-width="180"><template #default="{row}"><el-tooltip :content="tr(&quot;当前进程累计日志计数；DNS 报文表示请求解析错误，不等于上游 DNS 不可用；网络不可达 &quot;) + (row.process?.network_errors || 0) + tr(&quot;，本次服务累计退出 &quot;) + (row.process?.exits || 0)"><span>{{ tr("超时") }} {{ row.process?.timeouts || 0 }} {{ tr("· 拒绝") }} {{ row.process?.refused || 0 }} · TLS {{ row.process?.tls_failures || 0 }} {{ tr("· 代理拒绝") }} {{ row.process?.proxy_rejections || 0 }} {{ tr("· DNS 报文") }} {{ row.process?.dns_parse_errors || 0 }}</span></el-tooltip></template></el-table-column>
      <el-table-column min-width="130" :label='tr("最近检测")'><template #default="{ row }">{{ row.checked_at && !row.checked_at.startsWith('0001') ? dateTime(row.checked_at) : tr("等待检测") }}</template></el-table-column>
      <el-table-column :label='tr("说明 / 实测")' min-width="220"><template #default="{row}"><p>{{row.error}}</p><p v-if="row.udp_policy === 'reject'" class="muted">{{ tr("策略：拒绝非 DNS UDP") }}</p><el-button :disabled="!!diagnosing" @click="diagnose(row.id)">{{ tr("映射转发实测") }}</el-button></template></el-table-column>
      <el-table-column :label='tr("自动恢复")' min-width="230"><template #default="{row}">
        <div>{{ {closed:tr("正常重试"),open:tr("冷却中"),'half-open':tr("试探观察中")}[row.recovery?.circuit] || tr("待检查") }} {{ tr("· 尝试") }} {{ row.recovery?.attempts || 0 }} {{ tr("· 失败") }} {{ row.recovery?.failures || 0 }}</div>
        <div class="muted">{{ tr("下次重试") }} {{ date(row.recovery?.next_retry) }}</div>
        <el-button v-if="row.local_state !== 'healthy'" :disabled="!!retrying" :loading="retrying === row.id" @click="retry(row.id)">{{ tr("手动重试恢复") }}</el-button>
      </template></el-table-column>
    </el-table>
    <div class="mobile-list health-mobile">
     <el-empty v-if="!health.length" :description='tr("暂无活动映射")' />
     <article v-for="row in health" :key="row.id" class="monitor-item">
      <div class="item-title"><span>{{ row.tun_name }}</span><el-tag :type="row.local_state === 'healthy' ? 'success' : 'warning'">{{ tr("本地") }}{{ healthLabels[row.local_state] || tr("检测中") }}</el-tag></div>
      <div class="monitor-protocols"><details v-for="protocol in ['tcp','udp','dns']" :key="protocol"><summary><strong>{{ protocol.toUpperCase() }}</strong><span>{{ protocolLabel(row[protocol]) }}</span></summary><p>{{ protocolDetail(row[protocol]) }}</p></details></div>
      <p v-if="row.error || row.recovery_error" class="error-text">{{ row.recovery_error || row.error }}</p>
      <details><summary>{{ tr("解析器与错误计数") }}</summary><p>{{ tr("实际 DNS：") }}{{ row.resolver?.active || tr("等待启动") }} {{ tr("· 备用") }} {{ row.resolver?.fallbacks || 0 }} {{ tr("· 失败") }} {{ row.resolver?.failures || 0 }}</p><p>{{ tr("超时") }} {{ row.process?.timeouts || 0 }} {{ tr("· 代理拒绝") }} {{ row.process?.proxy_rejections || 0 }} {{ tr("· DNS 报文") }} {{ row.process?.dns_parse_errors || 0 }} · TLS {{ row.process?.tls_failures || 0 }} {{ tr("· 拒绝") }} {{ row.process?.refused || 0 }} {{ tr("· 网络不可达") }} {{ row.process?.network_errors || 0 }}</p></details>
      <p class="muted">{{healthLabels[row.state] || row.state}} {{ tr("· 最近检测") }} {{ date(row.checked_at) }}</p><p v-if="row.udp_policy === 'reject'" class="muted">{{ tr("策略：拒绝非 DNS UDP") }}</p><el-button :disabled="!!diagnosing" @click="diagnose(row.id)">{{ tr("映射转发实测") }}</el-button>
      <template v-if="row.local_state !== 'healthy'"><p class="muted">{{ tr("恢复尝试") }} {{ row.recovery?.attempts || 0 }} {{ tr("· 失败") }} {{ row.recovery?.failures || 0 }} {{ tr("· 下次") }} {{ date(row.recovery?.next_retry) }}</p><el-button :disabled="!!retrying" :loading="retrying === row.id" @click="retry(row.id)">{{ tr("重试恢复") }}</el-button></template>
     </article>
    </div>

    <details class="muted"><summary>{{ tr("检测与恢复说明") }}</summary><p>{{ tr("错误数为当前进程累计值。") }}</p>
     <p>{{ tr("DNS 为独立探测，“实际 DNS”为映射当前解析器。拒绝 UDP 策略下，UDP 不支持不影响映射正常判断。UDP 确认不支持或不可用后每 10 分钟复测；节点网络测试可立即复测。") }}</p>
     <p>{{ tr("本地恢复在 10 分钟内最多尝试 3 次，冷却 5 分钟；稳定 2 分钟后重置。上游异常不会触发隧道重启，手动重试会重置预算。") }}</p>
    </details>
  </el-card>
  <MonitoringDetails :active="active" :status="status"/>
  <el-dialog :model-value="!!diagnosing || !!diagnostic || !!diagnosticError" :title='tr("映射转发实测")' class="mapping-diagnostic" width="min(640px, calc(100vw - 24px))" append-to-body :close-on-click-modal="false" @close="closeDiagnostic"><p v-if="diagnosing">{{ tr("正在通过隔离入口检测映射转发…") }}</p><el-alert v-if="diagnosticError" :title="diagnosticError" type="error" :closable="false"/><template v-if="diagnostic"><el-tag :type="diagnostic.ok ? 'success' : 'warning'">{{diagnostic.ok ? tr("映射转发正常") : tr("检测完成，请查看分项")}}</el-tag><p>{{ tr("规则检查：") }}{{healthLabels[diagnostic.rules.state]}}</p><p>{{ tr("HTTPS 转发：") }}{{healthLabels[diagnostic.forwarding.state]}} · {{diagnostic.forwarding.duration_ms}} ms</p><p v-if="diagnostic.forwarding.exit_ip">{{ tr("映射出口 IP：") }}{{diagnostic.forwarding.exit_ip}}</p><p v-if="diagnostic.node?.exit_ip">{{ tr("节点出口 IP：") }}{{diagnostic.node.exit_ip}}</p><p v-if="diagnostic.exit_comparison === 'different'">{{ tr("两次出口观测不同；上游可能采用动态出口，请结合节点配置核对。") }}</p><p v-if="diagnostic.node?.error">{{diagnostic.node.error}}</p><p v-if="diagnostic.forwarding.error">{{diagnostic.forwarding.error}}</p><p class="muted">{{diagnostic.scope}}</p></template><template #footer><el-button @click="closeDiagnostic">{{diagnosing ? tr("停止检测") : tr("关闭")}}</el-button></template></el-dialog>
  <div class="monitor-secondary"><section><el-card shadow="never" class="section-gap"
        ><template #header>{{ tr("运行进程") }}</template
        ><el-table scrollbar-always-on :data="pids" :empty-text='tr("无运行实例")'
          ><el-table-column min-width="130" prop="tun" :label='tr("接口")' /><el-table-column min-width="130"
            prop="pid"
            label="PID" /></el-table></el-card></section
    ><section><el-card shadow="never"
        ><template #header>{{ tr("接口流量") }}</template
        ><el-table scrollbar-always-on class="desktop-list" :data="status.tun_traffic" :empty-text='tr("暂无流量")'
          ><el-table-column
            prop="tun_name"
            :label='tr("接口")'
            min-width="100"
          /><el-table-column :label='tr("接收")' min-width="100"
            ><template #default="{ row }">{{
              formatBytes(row.rx_bytes)
            }}</template></el-table-column
          ><el-table-column :label='tr("发送")' min-width="100"
            ><template #default="{ row }">{{
              formatBytes(row.tx_bytes)
            }}</template></el-table-column
          ><el-table-column :label='tr("丢包 / 错误")' min-width="160"><template #default="{row}">{{ (row.rx_dropped || 0) + (row.tx_dropped || 0) }} / {{ (row.rx_errors || 0) + (row.tx_errors || 0) }}<div v-if="row.error" class="error-text">{{ row.error }}</div></template></el-table-column></el-table
        >
        <div class="mobile-list"><el-empty v-if="!status.tun_traffic?.length" :description='tr("暂无流量")' /><article v-for="row in status.tun_traffic" :key="row.tun_name" class="monitor-item"><strong>{{ row.tun_name }}</strong><div class="item-details"><span>{{ tr("接收") }}</span><span>{{ formatBytes(row.rx_bytes) }}</span><span>{{ tr("发送") }}</span><span>{{ formatBytes(row.tx_bytes) }}</span><span>{{ tr("丢包 / 错误") }}</span><span>{{ (row.rx_dropped || 0) + (row.tx_dropped || 0) }} / {{ (row.rx_errors || 0) + (row.tx_errors || 0) }}</span></div><p v-if="row.error" class="error-text">{{ row.error }}</p></article></div>
        </el-card
      ></section
    ></div
  >
 </section>
</template>

<style scoped>
.runtime-monitor { display:grid;grid-template-columns:minmax(0,1fr);gap:var(--section-gap);min-width:0;max-width:100%; }
.runtime-monitor > *, .monitor-secondary > * { min-width:0;max-width:100%; }
.monitor-secondary { display:grid;grid-template-columns:minmax(0,1fr) minmax(0,2fr);gap:var(--section-gap); }
@media(max-width:991px){.monitor-secondary{grid-template-columns:minmax(0,1fr)}}
.runtime-monitor > .el-card { margin:0; }
.mobile-table-toggle { display:none; }
@media(max-width:767px){.mobile-table-toggle{display:flex;justify-content:flex-end}.show-tables :deep(.desktop-list){display:block}.show-tables :deep(.mobile-list){display:none}}
:deep(.el-scrollbar__wrap){overscroll-behavior-x:contain;}
:deep(.el-scrollbar__bar.is-horizontal){height:8px;}

:global(.mapping-diagnostic){max-height:calc(100dvh - 32px);overflow:auto;overflow-wrap:anywhere;}
@media(max-width:600px){:global(.mapping-diagnostic){margin:12px auto!important;max-height:calc(100dvh - 24px);}}
.monitor-item{padding:16px 0;border-bottom:1px solid var(--el-border-color-lighter);min-width:0;overflow-wrap:anywhere;}
.monitor-item:last-child{border-bottom:0;}
.monitor-item summary{cursor:pointer;font-size:14px;}
.monitor-item p{font-size:14px;}
.monitor-protocols{display:grid;gap:10px;margin:12px 0;}
.monitor-protocols summary strong{display:inline-block;width:48px;}
.monitor-protocols details p{color:var(--el-text-color-secondary);}
.monitor-item>.item-details{margin-top:12px;margin-bottom:0;}
</style>

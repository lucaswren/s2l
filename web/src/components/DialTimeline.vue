<script setup>
import { onBeforeUnmount, onMounted, ref } from "vue";
import { api } from "../api/client";
import { dateTime, tr } from "../i18n/index.js";
import { healthLabels } from "../utils/health";
import { validSampleTime } from "../utils/runtime";

defineProps({ mapping: Object, probe: Object });

const events = ref([]);
const listeners = ref(null);
const available = ref(null);
const busy = ref(false);
const error = ref("");
let controller;
const labels = {
  "ike-udp": () => tr("日志记录到入站 IKE UDP"),
  "ike-sa": () => tr("IKE 安全关联已建立"),
  "ike-failed": () => tr("IKE 协商或认证失败"),
  "ipsec-sa": () => tr("IPsec 数据安全关联已建立"),
  "l2tp-control": () => tr("L2TP 控制隧道已建立"),
  "l2tp-call": () => tr("L2TP 会话已建立"),
  "ppp-auth-ok": () => tr("PPP 认证成功"),
  "ppp-auth-failed": () => tr("PPP 认证失败"),
  "ppp-address": () => tr("PPP 已分配对端地址"),
  "ppp-closed": () => tr("PPP 会话已结束"),
};
async function refresh() {
  if (busy.value) return;
  controller = new AbortController();
  busy.value = true;
  error.value = "";
  try {
    const result = await api.dialTimeline(controller.signal);
    available.value = result.available;
    listeners.value = Array.isArray(result.udp_listeners) ? result.udp_listeners : null;
    events.value = Array.isArray(result.events) ? result.events.filter(e => Object.hasOwn(labels, e.stage)).reverse() : [];
  } catch (e) {
    if (e.name !== "AbortError") error.value = e.message;
  } finally {
    busy.value = false;
  }
}
onMounted(refresh);
onBeforeUnmount(() => controller?.abort());
</script>

<template>
  <section class="dial-timeline">
    <p class="muted">{{ tr("显示本次启动最近 30 分钟的拨号日志。多条会话可能交错。没有记录时，仍需用入站抓包确认 UDP 是否到达。") }}</p>
    <p v-if="listeners !== null">{{ tr("当前 UDP 监听：{0}", listeners.length ? listeners.join(", ") : "—") }}</p>
    <p v-if="mapping && validSampleTime(probe?.checked_at) !== null" class="muted">
      {{ tr("独立出口探测：{0} · {1} · {2}", mapping.tun_name, healthLabels[probe.state] ?? "—", dateTime(probe.checked_at)) }}
    </p>
    <el-button :loading="busy" @click="refresh">{{ tr("刷新") }}</el-button>
    <el-alert v-if="error" class="section-gap" type="error" :title="error" :closable="false" />
    <el-empty v-else-if="!busy && available === false" :description='tr("拨号日志不可用")' />
    <el-empty v-else-if="!busy && !events.length" :description='tr("暂无拨号事件")' />
    <el-timeline v-else class="section-gap" aria-live="polite">
      <el-timeline-item v-for="(event, index) in events" :key="index"
        :type="event.stage.endsWith('-failed') ? 'danger' : 'primary'"
        :timestamp="dateTime(event.at)">
        {{ labels[event.stage]() }}
      </el-timeline-item>
    </el-timeline>
  </section>
</template>

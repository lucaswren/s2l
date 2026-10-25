<script setup>
import { tr } from '../i18n/index.js';

import { computed, onUnmounted, reactive, ref, watch } from "vue";
import { ElMessageBox } from "element-plus";
import MappingActions from "./MappingActions.vue";
import {mappingGroups,mappingGroupKey} from '../utils/mapping-groups';
import { api } from "../api/client";

const props = defineProps({
  nodes: { type: Array, default: () => [] },
  mappings: { type: Array, default: () => [] },
  allMappings: { type: Array, default: () => [] },
  health: {type:Object,default:() => ({})},
});
const emit = defineEmits(["changed", "toast"]);

const mode = ref(""); // '' | edit | import
const editingId = ref("");
const editRevision=ref("");
const busy = ref(false);
const formRef = ref();
const required = { required: true, get message() { return tr("请填写此项"); }, trigger: "blur" };
const actionId = ref("");
const filter = ref("all");const groupFilter=ref("*");
const groups=computed(()=>mappingGroups(props.mappings));
const shownMappings = computed(() => props.mappings.filter(m => {
  if(groupFilter.value!=='*' && mappingGroupKey(m)!==groupFilter.value)return false;
  if (filter.value === 'all') return true;
  if (filter.value === 'abnormal') return m.status === 'error' || (m.status === 'running' && props.health[m.id] && !['healthy','checking'].includes(props.health[m.id].state));
  return m.status === filter.value;
}));
const batchBusy = ref(false);
const batchConfirming = ref(false);
const batchResults = ref([]);
let cancelBatch = false;
let unmounted = false;
const actionsBusy = computed(() => batchBusy.value || batchConfirming.value ? 'batch' : actionId.value);
onUnmounted(() => {unmounted = true; cancelBatch = true;});
async function batch(act) {
  if (batchBusy.value || batchConfirming.value || actionId.value) return;
  const targets = shownMappings.value.filter(m => act === 'start' ? m.status !== 'running' : m.status !== 'stopped');
  if (!targets.length) return;
  batchConfirming.value = true;
  try { await ElMessageBox.confirm(tr("将依次{0}当前筛选列表中的 {1} 条映射。可取消尚未执行的任务。", act === 'start' ? tr("启动") : tr("停止"), targets.length), tr("批量操作"), {get confirmButtonText() { return tr("执行"); },get cancelButtonText() { return tr("取消"); },type:'warning'}); }
  catch { return; }
  finally { batchConfirming.value = false; }
  if (unmounted || batchBusy.value || actionId.value) return;
  batchBusy.value = true; cancelBatch = false;
  batchResults.value = targets.map(m => ({id:m.id,tun:m.tun_name,state:'排队',error:''}));
  try {
    for (const row of batchResults.value) {
      if (cancelBatch) {row.state = '已取消'; continue;}
      row.state = '执行中';
      try {await api.mappingAction(row.id,act); row.state = '成功';}
      catch(e) {row.state = '失败';row.error = e.message;}
      if (!unmounted) emit('changed');
    }
  } finally {batchBusy.value = false;}
}
async function copyErrors() {
  const rows = batchResults.value.filter(r => r.error).map(r => `${r.tun} (${r.id}): ${r.error}`);
  if (!rows.length) rows.push(...shownMappings.value.filter(m => m.error_msg).map(m => `${m.tun_name}: ${m.error_msg}`));
  try {await navigator.clipboard.writeText(rows.join('\n'));emit('toast',{type:'ok',get message() { return tr("错误详情已复制"); }});}
  catch {emit('toast',{type:'err',get message() { return tr("浏览器不允许复制，请手动选中错误详情"); }});}
}
const form = reactive({
  node_id: "",
  tun_name: "tun101",
  table_id: 101,
  subnet: "10.0.101.0/24",
  l2tp_user: "",
  l2tp_pass: "",
  observe_connections:false,group:"",upload_kbps:0,download_kbps:0,max_connections:0,max_sessions:1,mtu: 1400, mss: 1200, queue_length: 1000, udp_policy: "reject", dns_mode: "auto", dns_doh_url: "", dns_tcp_address: "",
});
const importText = ref("");
const importErrors = ref([]);
const mapFormats = [
  tr("table_id,节点,l2tp账号,l2tp密码"),
  tr("table_id,节点,tun,subnet,账号,密码"),
];

const nextTable = computed(() => {
  const used = new Set(props.allMappings.map((m) => m.table_id));
  let id = 101;
  while (used.has(id) && id <= 252) id++;
  if (id <= 252) return id;
  return (
    Array.from({ length: 100 }, (_, i) => i + 1).find((id) => !used.has(id)) ||
    0
  );
});

watch(
  () => props.nodes,
  (list) => {
    if (!form.node_id && list.length) form.node_id = list[0].id;
  },
  { immediate: true },
);

const nodeName = computed(() => {
  const map = Object.fromEntries(props.nodes.map((n) => [n.id, n.name]));
  return (id) => map[id] || id;
});

function applyPreset() {
  const id = Number(form.table_id) || nextTable.value;
  form.table_id = id;
  form.tun_name = `tun${id}`;
  form.subnet = `10.0.${id % 256}.0/24`;
  if (!form.l2tp_user) form.l2tp_user = `l2tp${id}`;
}

function resetForm() {
  editingId.value = "";editRevision.value="";
  const id = nextTable.value;
  Object.assign(form, {
    node_id: props.nodes[0]?.id || "",
    table_id: id,
    tun_name: `tun${id}`,
    subnet: `10.0.${id % 256}.0/24`,
    l2tp_user: `l2tp${id}`,
    l2tp_pass: "",
  observe_connections:false,group:"",upload_kbps:0,download_kbps:0,max_connections:0,max_sessions:1,mtu: 1400, mss: 1200, queue_length: 1000, udp_policy: "reject", dns_mode: "auto", dns_doh_url: "", dns_tcp_address: "",
  });
}

function openCreate() {
  if (!nextTable.value) {
    emit("toast", { type: "err", get message() { return tr("路由表编号已用完"); } });
    return;
  }
  resetForm();
  mode.value = "edit";
}

async function openEdit(m) {
  try {
    m = await api.getMapping(m.id);
  } catch (e) {
    emit("toast", { type: "err", message: e.message });
    return;
  }
  editingId.value = m.id;editRevision.value=m._revision||"";
  Object.assign(form, {
    observe_connections:!!m.observe_connections,node_id: m.node_id,group:m.group||"",upload_kbps:m.upload_kbps||0,download_kbps:m.download_kbps||0,max_connections:m.max_connections||0,max_sessions:m.max_sessions||0,
    tun_name: m.tun_name,
    table_id: m.table_id,
    subnet: m.subnet,
    l2tp_user: m.l2tp_user || "",
    l2tp_pass: m.l2tp_pass || "",
    mtu: m.mtu || 1400, mss: m.mss || 1200, queue_length: m.queue_length || 1000, udp_policy: m.udp_policy || "reject", dns_mode: m.dns_mode || "auto", dns_doh_url: m.dns_doh_url || "", dns_tcp_address: m.dns_tcp_address || "",
  });
  mode.value = "edit";
}

function openImport() {
  importText.value = "";
  importErrors.value = [];
  mode.value = "import";
}

function close() {
  mode.value = "";
  editingId.value = "";editRevision.value="";
}

async function save() {
  if (!(await formRef.value.validate().catch(() => false))) return;
  busy.value = true;
  try {
    const body = {
      ...form,
      table_id: Number(form.table_id),
    };
    if (editingId.value) body.id = editingId.value;
    const preview=await api.previewMapping(body,editRevision.value);
    const labels={get dns_doh_url(){return tr("DoH 地址")},get dns_tcp_address(){return tr("TCP DNS 地址")},get observe_connections(){return tr("连接观测")},get group() { return tr("分组"); },get name() { return tr("名称"); },get addr() { return tr("节点地址"); },get username() { return tr("代理账号"); },get password() { return tr("代理密码"); },get node_id() { return tr("出口节点"); },tun_name:'TUN',get table_id() { return tr("路由表"); },get subnet() { return tr("子网"); },get l2tp_user() { return tr("拨号账号"); },get l2tp_pass() { return tr("拨号密码"); },mtu:'MTU',mss:'MSS',get queue_length() { return tr("队列长度"); },get udp_policy() { return tr("UDP 策略"); },get dns_mode() { return tr("DNS 模式"); },get upload_kbps() { return tr("上传限速"); },get download_kbps() { return tr("下载限速"); },get max_connections() { return tr("连接上限"); },get max_sessions() { return tr("PPP 会话上限"); }};
    await ElMessageBox.confirm(tr("变更：{0}。影响 {1} 条映射。{2}", preview.fields.map(f=>labels[f]||f).join('、')||tr("无"), preview.affected.length, preview.message),tr("保存配置"),{get confirmButtonText() { return tr("保存"); },get cancelButtonText() { return tr("返回编辑"); }});
    await api.saveMapping(body,editRevision.value);
    emit("toast", {
      type: "ok",
      message: editingId.value ? tr("映射已更新") : tr("映射已创建"),
    });
    close();
    emit("changed");
  } catch (e) {
    if(e==='cancel'||e==='close')return;
    emit("toast", { type: "err", message: e.message });
  } finally {
    busy.value = false;
  }
}

async function doImport() {
  busy.value = true;
  try {
    const res = await api.importMappings({ text: importText.value });
    importErrors.value = res.errors || [];
    const msg =
      tr("导入完成：新增 {0}，更新 {1}", res.created || 0, res.updated || 0) +
      (res.skipped ? tr("，跳过 {0}", res.skipped) : "");
    emit("toast", {
      type: res.errors?.length ? "err" : "ok",
      message: res.errors?.length
        ? `${msg}；${res.errors.slice(0, 3).join("；")}`
        : msg,
    });
    if (!res.errors?.length) close();
    emit("changed");
  } catch (e) {
    emit("toast", { type: "err", message: e.message });
  } finally {
    busy.value = false;
  }
}

async function action(id, act) {
  if (actionsBusy.value) return;
  actionId.value = id + act;
  try {
    await api.mappingAction(id, act);
    emit("toast", {
      type: "ok",
      message: act === "start" ? tr("已启动") : tr("已停止"),
    });
    emit("changed");
  } catch (e) {
    emit("toast", { type: "err", message: e.message });
  } finally {
    actionId.value = "";
  }
}

async function remove(id) {
  if (actionsBusy.value) return;
  try {
    await ElMessageBox.confirm(tr("删除该映射及其拨号账号？"), tr("删除映射"), {
      type: "warning",
      get confirmButtonText() { return tr("删除"); },
      get cancelButtonText() { return tr("取消"); },
    });
  } catch {
    return;
  }
  try {
    await api.deleteMapping(id);
    emit("toast", { type: "ok", get message() { return tr("映射已删除"); } });
    emit("changed");
  } catch (e) {
    emit("toast", { type: "err", message: e.message });
  }
}

const editingActive = computed(() => {
  if (!editingId.value) return false;
  const m = props.allMappings.find((x) => x.id === editingId.value);
  return m && m.status !== "stopped";
});
</script>
<template>
  <section>
    <div class="section-head">
      <div>
        <h2>{{ tr("隧道映射") }}</h2>

      </div>
      <div class="actions">
        <el-button :disabled="!nodes.length || !!actionsBusy" @click="openImport"
          >{{ tr("批量导入") }}</el-button
        ><el-button type="primary" :disabled="!nodes.length || !!actionsBusy" @click="openCreate"
          >{{ tr("新建映射") }}</el-button
        >
      </div>
    </div>
    <div class="actions section-gap">
      <el-select v-model="filter" style="width: 150px" :aria-label='tr("映射状态筛选")'>
        <el-option :label='tr("全部状态")' value="all" /><el-option :label='tr("运行中")' value="running" /><el-option :label='tr("已停止")' value="stopped" /><el-option :label='tr("异常链路")' value="abnormal" />
      </el-select>
      <el-select v-model="groupFilter" style="width:150px" :aria-label='tr("映射分组筛选")'><el-option value="*" :label='tr("全部分组")'/><el-option v-for="g in groups" :key="g.value" :value="g.value" :label="g.label"/></el-select>
 <el-button :disabled="!!actionsBusy" @click="batch('start')">{{ tr("批量启动当前列表") }}</el-button>
      <el-button :disabled="!!actionsBusy" @click="batch('stop')">{{ tr("批量停止当前列表") }}</el-button>
      <el-button v-if="batchBusy" @click="cancelBatch = true">{{ tr("取消排队任务") }}</el-button>
      <el-button @click="copyErrors">{{ tr("复制错误详情") }}</el-button>
    </div>
    <el-table v-if="batchResults.length" :data="batchResults" max-height="260" class="section-gap batch-results">
      <el-table-column prop="tun" :label='tr("接口")' /><el-table-column :label='tr("任务状态")'><template #default="{row}">{{tr(row.state)}}</template></el-table-column><el-table-column prop="error" :label='tr("失败详情")' min-width="250" />
    </el-table>
    <el-alert
      v-if="!nodes.length"
      :title='tr("请先在下方添加或导入 SOCKS5 节点。")'
      type="info"
      show-icon
      :closable="false"
      class="section-gap"
    />
    <el-table :data="shownMappings" class="desktop-list" :empty-text='tr("暂无映射")'
      ><el-table-column prop="tun_name" :label='tr("TUN / 路由表")' min-width="130"
        ><template #default="{ row }"
          ><strong>{{ row.tun_name }}</strong>
          <div class="muted">{{ tr("路由表") }} {{ row.table_id }}</div></template
        ></el-table-column
      ><el-table-column
        prop="subnet"
        :label='tr("子网")'
        min-width="155" /><el-table-column :label='tr("节点")' min-width="140"
        ><template #default="{ row }">{{
          nodeName(row.node_id)
        }}</template></el-table-column
      ><el-table-column
        prop="l2tp_user"
        :label='tr("L2TP 账号")'
        min-width="120" /><el-table-column :label='tr("状态")' min-width="220"
        ><template #default="{ row }"
          ><el-tag
            :type="
              row.status === 'running'
                ? 'success'
                : row.status === 'error'
                  ? 'danger'
                  : 'info'
            "
            >{{
              row.status === "running"
                ? tr("运行中")
                : row.status === "error"
                  ? tr("操作失败")
                  : tr("已停止")
            }}</el-tag
          >
          <div v-if="row.error_msg" class="error-text">
            {{ row.error_msg }}
          </div></template
        ></el-table-column
      ><el-table-column :label='tr("操作")' min-width="280"
        ><template #default="{ row }"
          ><MappingActions
            :mapping="row"
            :action-id="actionsBusy"
            @edit="openEdit"
            @action="action"
            @remove="remove" /></template></el-table-column
    ></el-table>
    <div class="mobile-list">
      <el-empty v-if="!shownMappings.length" :description='tr("暂无映射")' /><el-card
        v-for="m in shownMappings"
        :key="m.id"
        shadow="never"
        ><div class="item-title">
          <span>{{ m.tun_name }}</span
          ><el-tag
            :type="
              m.status === 'running'
                ? 'success'
                : m.status === 'error'
                  ? 'danger'
                  : 'info'
            "
            >{{
              m.status === "running"
                ? tr("运行中")
                : m.status === "error"
                  ? tr("操作失败")
                  : tr("已停止")
            }}</el-tag
          >
        </div>
        <div class="item-details">
          <span>{{ tr("节点") }}</span><span>{{ nodeName(m.node_id) }}</span
          ><span>{{ tr("路由表") }}</span><span>{{ m.table_id }}</span
          ><span>{{ tr("子网") }}</span><span class="mono">{{ m.subnet }}</span
          ><span>{{ tr("L2TP 账号") }}</span><span>{{ m.l2tp_user }}</span>
        </div>
        <el-alert
          v-if="m.error_msg"
          :title="m.error_msg"
          type="error"
          :closable="false"
          class="section-gap" /><MappingActions
          :mapping="m"
          :action-id="actionsBusy"
            @edit="openEdit"
          @action="action"
          @remove="remove"
      /></el-card>
    </div>
    <el-dialog
      :model-value="mode === 'edit'"
      :title="editingId ? tr(&quot;编辑映射&quot;) : tr(&quot;新建映射&quot;)"
      width="680px"
      class="edit-dialog"
      :close-on-click-modal="false"
      :close-on-press-escape="!busy"
      :show-close="!busy"
      @close="close"
      ><el-alert
        v-if="editingActive"
        :title='tr("请先停止映射；异常映射需清理成功后再修改配置。")'
        type="warning"
        :closable="false"
        class="section-gap"
      /><el-form
        ref="formRef"
        :model="form"
        label-position="top"
        :disabled="busy || editingActive"
        @submit.prevent="save"
        ><div class="form-grid">
          <el-form-item
            :label='tr("SOCKS5 节点")'
            prop="node_id"
            :rules="required"
            class="form-wide"
            ><el-select
              v-model="form.node_id"
              filterable
              :disabled="editingActive"
              ><el-option
                v-for="n in nodes"
                :key="n.id"
                :label="n.name + ' (' + n.addr + ')'"
                :value="n.id" /></el-select></el-form-item
          ><el-form-item
            :label='tr("路由表编号（1–252）")'
            prop="table_id"
            :rules="required"
            ><el-input-number
              v-model="form.table_id"
              :min="1"
              :max="252"
              :disabled="editingActive" /></el-form-item
          ><el-form-item :label='tr("TUN 接口")' prop="tun_name" :rules="required"
            ><el-input
              v-model="form.tun_name"
              :disabled="editingActive" /></el-form-item
          ><el-form-item
            :label='tr("IPv4 子网")'
            prop="subnet"
            :rules="required"
            class="form-wide"
            ><el-input
              v-model="form.subnet"
              :disabled="editingActive"
              placeholder="10.0.101.0/24" /></el-form-item
          ><el-form-item :label='tr("L2TP 账号")' prop="l2tp_user" :rules="required"
            ><el-input
              v-model="form.l2tp_user"
              :disabled="editingActive" /></el-form-item
          ><el-form-item :label='tr("L2TP 密码")' prop="l2tp_pass" :rules="required"
            ><el-input
              v-model="form.l2tp_pass"
              type="password"
              show-password
              :readonly="editingActive"
              autocomplete="new-password"
          /></el-form-item>
          <details class="form-wide mapping-advanced"><summary>{{ tr("高级选项 · 分组、限额与网络参数") }}<span v-if="form.upload_kbps || form.download_kbps || form.max_connections"> {{ tr("· 限额已启用") }}</span></summary><div class="form-grid"><el-form-item :label='tr("分组")'><el-input v-model="form.group" maxlength="64"/></el-form-item>
 <el-form-item :label='tr("上传上限 Kbit/s")'><el-input-number v-model="form.upload_kbps" :min="0" :max="10000000" :disabled="editingActive"/></el-form-item>
 <el-form-item :label='tr("下载上限 Kbit/s")'><el-input-number v-model="form.download_kbps" :min="0" :max="10000000" :disabled="editingActive"/></el-form-item>
 <el-form-item :label='tr("拨号保护")'><span>{{tr("同一账号仅允许一条拨号会话；多设备可共享网关。")}}</span></el-form-item>
 <el-form-item :label='tr("连接观测")'><el-switch v-model="form.observe_connections" :disabled="editingActive"/></el-form-item><el-form-item :label='tr("TCP 连接上限")'><el-input-number v-model="form.max_connections" :min="0" :max="1000000" :disabled="editingActive"/></el-form-item>
 <p class="muted form-wide">{{ tr("0 表示不限；限额按整条映射合计。下载超额采用丢弃控制速率，非平滑整形。") }}</p>
 <el-form-item label="MTU（1280–1400）"><el-input-number v-model="form.mtu" :min="1280" :max="1400" :disabled="editingActive" /></el-form-item>
          <el-form-item label="TCP MSS"><el-input-number v-model="form.mss" :min="536" :max="form.mtu - 40" :disabled="editingActive" /></el-form-item>
          <el-form-item :label='tr("接口队列长度")'><el-input-number v-model="form.queue_length" :min="100" :max="10000" :disabled="editingActive" /></el-form-item>
          <el-form-item :label='tr("UDP 策略")'><el-select v-model="form.udp_policy" :disabled="editingActive"><el-option value="reject-quic" :label='tr("拒绝 UDP/443，支持时回退 TCP")' /><el-option value="allow" :label='tr("允许 UDP（需上游支持）")' /><el-option value="reject" :label='tr("主动拒绝 UDP（DNS 走 DoH/TCP，推荐）")' /></el-select></el-form-item>
          <el-form-item :label='tr("DNS 模式")' class="form-wide"><el-select v-model="form.dns_mode" :disabled="editingActive"><el-option value="auto" :label='tr("自动：DoH 故障时切换 TCP DNS")' /><el-option value="doh" :label='tr("仅 DoH")' /><el-option value="tcp" :label='tr("仅 TCP DNS")' /></el-select></el-form-item>
          <el-form-item :label='tr("DoH 地址")' class="form-wide"><el-input v-model.trim="form.dns_doh_url" name="dns_doh_url" maxlength="2048" placeholder="https://1.1.1.1/dns-query" :disabled="editingActive" /></el-form-item>
          <el-form-item :label='tr("TCP DNS 地址")' class="form-wide"><el-input v-model.trim="form.dns_tcp_address" name="dns_tcp_address" maxlength="320" placeholder="8.8.8.8:53" :disabled="editingActive" /></el-form-item>
          <p class="muted form-wide">{{ tr("留空使用默认解析器。DoH 必须是无账号、查询参数和片段的 HTTPS URL；TCP DNS 填写主机:端口，IPv6 地址加方括号。解析器域名和所有 DNS 查询均经当前 SOCKS5。") }}</p>
          <p class="muted form-wide">{{ tr("自动模式仅在同一节点内切换 DoH/TCP，不切换节点或直连。自定义 DNS 不改变客户端收到的 DNS 地址；进入隧道的 53 端口请求会使用这里的解析器。") }}</p>
          <p class="muted form-wide">{{ tr("默认 MTU 1400、MSS 1200。减小 MTU 时请同步调整 MSS；允许 UDP 无法为不支持 UDP 的节点增加能力。") }}</p>
          </div></details></div></el-form
      ><template #footer
        ><el-button v-if="!editingId" :disabled="busy" @click="applyPreset"
          >{{ tr("按路由表填充") }}</el-button
        ><el-button :disabled="busy" @click="close">{{ tr("关闭") }}</el-button
        ><el-button
          type="primary"
          :loading="busy"
          :disabled="editingActive"
          @click="save"
          >{{ tr("保存") }}</el-button
        ></template
      ></el-dialog
    >
    <el-dialog
      :model-value="mode === 'import'"
      :title='tr("批量导入映射")'
      width="680px"
      class="edit-dialog"
      :close-on-click-modal="false"
      :close-on-press-escape="!busy"
      :show-close="!busy"
      @close="close"
      ><div class="format-list">
        <el-tag v-for="f in mapFormats" :key="f" type="info">{{ f }}</el-tag>
      </div>
      <p class="muted"> {{ tr("每行一条；节点可用名称、ID 或地址；简写自动生成接口与子网。") }} </p>
      <el-input
        v-model="importText"
        :disabled="busy"
        type="textarea"
        :rows="8"
        :placeholder='tr("101,线路A,user101,pass101")'
        :aria-label='tr("批量导入映射")'
      /><el-alert
        v-if="importErrors.length"
        class="import-errors"
        type="error"
        :title='tr("以下行未导入")'
        :closable="false"
        ><ul>
          <li v-for="(err, i) in importErrors" :key="i">{{ err }}</li>
        </ul></el-alert
      ><template #footer
        ><el-button :disabled="busy" @click="close">{{ tr("取消") }}</el-button
        ><el-button
          type="primary"
          :loading="busy"
          :disabled="!importText.trim()"
          @click="doImport"
          >{{ tr("导入") }}</el-button
        ></template
      ></el-dialog
    >
  </section>
</template>

<style scoped>.mapping-advanced>summary{cursor:pointer;min-height:32px;line-height:1.6;padding:4px 0;font-size:14px}.mapping-advanced>.form-grid{margin-top:8px}</style>

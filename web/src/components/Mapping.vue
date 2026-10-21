<script setup>
import { computed, onUnmounted, reactive, ref, watch } from "vue";
import { ElMessageBox } from "element-plus";
import MappingActions from "./MappingActions.vue";
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
const required = { required: true, message: "请填写此项", trigger: "blur" };
const actionId = ref("");
const filter = ref("all");const groupFilter=ref("*");
const groups=computed(()=>[...new Set(props.mappings.map(m=>m.group||"未分组"))].sort());
const shownMappings = computed(() => props.mappings.filter(m => {
  if(groupFilter.value!=='*' && (m.group||'未分组')!==groupFilter.value)return false;
  if (filter.value === 'all') return true;
  if (filter.value === 'abnormal') return m.status === 'error' || (m.status === 'running' && props.health[m.id] && !['healthy','checking'].includes(props.health[m.id].state));
  return m.status === filter.value;
}));
const batchBusy = ref(false);
const batchResults = ref([]);
let cancelBatch = false;
let unmounted = false;
const actionsBusy = computed(() => batchBusy.value ? 'batch' : actionId.value);
onUnmounted(() => {unmounted = true; cancelBatch = true;});
async function batch(act) {
  if (batchBusy.value || actionId.value) return;
  const targets = shownMappings.value.filter(m => act === 'start' ? m.status !== 'running' : m.status !== 'stopped');
  if (!targets.length) return;
  try { await ElMessageBox.confirm(`将依次${act === 'start' ? '启动' : '停止'}当前筛选列表中的 ${targets.length} 条映射。可取消尚未执行的任务。`, '批量操作', {confirmButtonText:'执行',cancelButtonText:'取消',type:'warning'}); }
  catch { return; }
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
  try {await navigator.clipboard.writeText(rows.join('\n'));emit('toast',{type:'ok',message:'错误详情已复制'});}
  catch {emit('toast',{type:'err',message:'浏览器不允许复制，请手动选中错误详情'});}
}
const form = reactive({
  node_id: "",
  tun_name: "tun101",
  table_id: 101,
  subnet: "10.0.101.0/24",
  l2tp_user: "",
  l2tp_pass: "",
  group:"",upload_kbps:0,download_kbps:0,max_connections:0,max_sessions:0,mtu: 1400, mss: 1200, queue_length: 1000, udp_policy: "reject", dns_mode: "auto",
});
const importText = ref("");
const importErrors = ref([]);
const mapFormats = [
  "table_id,节点,l2tp账号,l2tp密码",
  "table_id,节点,tun,subnet,账号,密码",
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
  group:"",upload_kbps:0,download_kbps:0,max_connections:0,max_sessions:0,mtu: 1400, mss: 1200, queue_length: 1000, udp_policy: "reject", dns_mode: "auto",
  });
}

function openCreate() {
  if (!nextTable.value) {
    emit("toast", { type: "err", message: "路由表编号已用完" });
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
    node_id: m.node_id,group:m.group||"",upload_kbps:m.upload_kbps||0,download_kbps:m.download_kbps||0,max_connections:m.max_connections||0,max_sessions:m.max_sessions||0,
    tun_name: m.tun_name,
    table_id: m.table_id,
    subnet: m.subnet,
    l2tp_user: m.l2tp_user || "",
    l2tp_pass: m.l2tp_pass || "",
    mtu: m.mtu || 1400, mss: m.mss || 1200, queue_length: m.queue_length || 1000, udp_policy: m.udp_policy || "reject", dns_mode: m.dns_mode || "auto",
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
    const labels={group:'分组',name:'名称',addr:'节点地址',username:'代理账号',password:'代理密码',node_id:'出口节点',tun_name:'TUN',table_id:'路由表',subnet:'子网',l2tp_user:'拨号账号',l2tp_pass:'拨号密码',mtu:'MTU',mss:'MSS',queue_length:'队列长度',udp_policy:'UDP 策略',dns_mode:'DNS 模式',upload_kbps:'上传限速',download_kbps:'下载限速',max_connections:'连接上限',max_sessions:'PPP 会话上限'};
    await ElMessageBox.confirm(`变更：${preview.fields.map(f=>labels[f]||f).join('、')||'无'}。影响 ${preview.affected.length} 条映射。${preview.message}`,'保存配置',{confirmButtonText:'保存',cancelButtonText:'返回编辑'});
    await api.saveMapping(body,editRevision.value);
    emit("toast", {
      type: "ok",
      message: editingId.value ? "映射已更新" : "映射已创建",
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
      `导入完成：新增 ${res.created || 0}，更新 ${res.updated || 0}` +
      (res.skipped ? `，跳过 ${res.skipped}` : "");
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
      message: act === "start" ? "已启动" : "已停止",
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
    await ElMessageBox.confirm("删除该映射及其拨号账号？", "删除映射", {
      type: "warning",
      confirmButtonText: "删除",
      cancelButtonText: "取消",
    });
  } catch {
    return;
  }
  try {
    await api.deleteMapping(id);
    emit("toast", { type: "ok", message: "映射已删除" });
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
        <h2>隧道映射</h2>

      </div>
      <div class="actions">
        <el-button :disabled="!nodes.length || !!actionsBusy" @click="openImport"
          >批量导入</el-button
        ><el-button type="primary" :disabled="!nodes.length || !!actionsBusy" @click="openCreate"
          >新建映射</el-button
        >
      </div>
    </div>
    <div class="actions section-gap">
      <el-select v-model="filter" style="width: 150px" aria-label="映射状态筛选">
        <el-option label="全部状态" value="all" /><el-option label="运行中" value="running" /><el-option label="已停止" value="stopped" /><el-option label="异常链路" value="abnormal" />
      </el-select>
      <el-select v-model="groupFilter" style="width:150px" aria-label="映射分组筛选"><el-option value="*" label="全部分组"/><el-option v-for="g in groups" :key="g" :value="g" :label="g"/></el-select>
 <el-button :disabled="!!actionsBusy" @click="batch('start')">批量启动当前列表</el-button>
      <el-button :disabled="!!actionsBusy" @click="batch('stop')">批量停止当前列表</el-button>
      <el-button v-if="batchBusy" @click="cancelBatch = true">取消排队任务</el-button>
      <el-button @click="copyErrors">复制错误详情</el-button>
    </div>
    <el-table v-if="batchResults.length" :data="batchResults" max-height="260" class="section-gap">
      <el-table-column prop="tun" label="接口" /><el-table-column prop="state" label="任务状态" /><el-table-column prop="error" label="失败详情" min-width="250" />
    </el-table>
    <el-alert
      v-if="!nodes.length"
      title="请先在下方添加或导入 SOCKS5 节点。"
      type="info"
      show-icon
      :closable="false"
      class="section-gap"
    />
    <el-table :data="shownMappings" class="desktop-list" empty-text="暂无映射"
      ><el-table-column prop="tun_name" label="TUN / 路由表" min-width="130"
        ><template #default="{ row }"
          ><strong>{{ row.tun_name }}</strong>
          <div class="muted">路由表 {{ row.table_id }}</div></template
        ></el-table-column
      ><el-table-column
        prop="subnet"
        label="子网"
        min-width="155" /><el-table-column label="节点" min-width="140"
        ><template #default="{ row }">{{
          nodeName(row.node_id)
        }}</template></el-table-column
      ><el-table-column
        prop="l2tp_user"
        label="L2TP 账号"
        min-width="120" /><el-table-column label="状态" min-width="220"
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
                ? "运行中"
                : row.status === "error"
                  ? "操作失败"
                  : "已停止"
            }}</el-tag
          >
          <div v-if="row.error_msg" class="error-text">
            {{ row.error_msg }}
          </div></template
        ></el-table-column
      ><el-table-column label="操作" width="290" fixed="right"
        ><template #default="{ row }"
          ><MappingActions
            :mapping="row"
            :action-id="actionsBusy"
            @edit="openEdit"
            @action="action"
            @remove="remove" /></template></el-table-column
    ></el-table>
    <div class="mobile-list">
      <el-empty v-if="!shownMappings.length" description="暂无映射" /><el-card
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
                ? "运行中"
                : m.status === "error"
                  ? "操作失败"
                  : "已停止"
            }}</el-tag
          >
        </div>
        <div class="item-details">
          <span>节点</span><span>{{ nodeName(m.node_id) }}</span
          ><span>路由表</span><span>{{ m.table_id }}</span
          ><span>子网</span><span class="mono">{{ m.subnet }}</span
          ><span>L2TP 账号</span><span>{{ m.l2tp_user }}</span>
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
      :title="editingId ? '编辑映射' : '新建映射'"
      width="680px"
      class="edit-dialog"
      :close-on-click-modal="false"
      :close-on-press-escape="!busy"
      :show-close="!busy"
      @close="close"
      ><el-alert
        v-if="editingActive"
        title="请先停止映射；异常映射需清理成功后再修改配置。"
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
            label="SOCKS5 节点"
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
            label="路由表编号（1–252）"
            prop="table_id"
            :rules="required"
            ><el-input-number
              v-model="form.table_id"
              :min="1"
              :max="252"
              :disabled="editingActive" /></el-form-item
          ><el-form-item label="TUN 接口" prop="tun_name" :rules="required"
            ><el-input
              v-model="form.tun_name"
              :disabled="editingActive" /></el-form-item
          ><el-form-item
            label="IPv4 子网"
            prop="subnet"
            :rules="required"
            class="form-wide"
            ><el-input
              v-model="form.subnet"
              :disabled="editingActive"
              placeholder="10.0.101.0/24" /></el-form-item
          ><el-form-item label="L2TP 账号" prop="l2tp_user" :rules="required"
            ><el-input
              v-model="form.l2tp_user"
              :disabled="editingActive" /></el-form-item
          ><el-form-item label="L2TP 密码" prop="l2tp_pass" :rules="required"
            ><el-input
              v-model="form.l2tp_pass"
              type="password"
              show-password
              :readonly="editingActive"
              autocomplete="new-password"
          /></el-form-item>
          <details class="form-wide mapping-advanced"><summary>高级选项 · 分组、限额与网络参数<span v-if="form.upload_kbps || form.download_kbps || form.max_sessions || form.max_connections"> · 限额已启用</span></summary><div class="form-grid"><el-form-item label="分组"><el-input v-model="form.group" maxlength="64"/></el-form-item>
 <el-form-item label="上传上限 Kbit/s"><el-input-number v-model="form.upload_kbps" :min="0" :max="10000000" :disabled="editingActive"/></el-form-item>
 <el-form-item label="下载上限 Kbit/s"><el-input-number v-model="form.download_kbps" :min="0" :max="10000000" :disabled="editingActive"/></el-form-item>
 <el-form-item label="PPP 会话上限"><el-input-number v-model="form.max_sessions" :min="0" :max="128" :disabled="editingActive"/></el-form-item>
 <el-form-item label="TCP 连接上限"><el-input-number v-model="form.max_connections" :min="0" :max="1000000" :disabled="editingActive"/></el-form-item>
 <p class="muted form-wide">0 表示不限；限额按整条映射合计。下载超额采用丢弃控制速率，非平滑整形。</p>
 <el-form-item label="MTU（1280–1400）"><el-input-number v-model="form.mtu" :min="1280" :max="1400" :disabled="editingActive" /></el-form-item>
          <el-form-item label="TCP MSS"><el-input-number v-model="form.mss" :min="536" :max="form.mtu - 40" :disabled="editingActive" /></el-form-item>
          <el-form-item label="接口队列长度"><el-input-number v-model="form.queue_length" :min="100" :max="10000" :disabled="editingActive" /></el-form-item>
          <el-form-item label="UDP 策略"><el-select v-model="form.udp_policy" :disabled="editingActive"><el-option value="reject-quic" label="拒绝 UDP/443，支持时回退 TCP" /><el-option value="allow" label="允许 UDP（需上游支持）" /><el-option value="reject" label="主动拒绝 UDP（DNS 走 DoH/TCP，推荐）" /></el-select></el-form-item>
          <el-form-item label="DNS 模式" class="form-wide"><el-select v-model="form.dns_mode" :disabled="editingActive"><el-option value="auto" label="自动：DoH 故障时切换 TCP DNS" /><el-option value="doh" label="仅 DoH" /><el-option value="tcp" label="仅 TCP DNS" /></el-select></el-form-item>
          <p class="muted form-wide">默认 MTU 1400、MSS 1200。减小 MTU 时请同步调整 MSS；允许 UDP 无法为不支持 UDP 的节点增加能力。</p>
          </div></details></div></el-form
      ><template #footer
        ><el-button v-if="!editingId" :disabled="busy" @click="applyPreset"
          >按路由表填充</el-button
        ><el-button :disabled="busy" @click="close">关闭</el-button
        ><el-button
          type="primary"
          :loading="busy"
          :disabled="editingActive"
          @click="save"
          >保存</el-button
        ></template
      ></el-dialog
    >
    <el-dialog
      :model-value="mode === 'import'"
      title="批量导入映射"
      width="680px"
      class="edit-dialog"
      :close-on-click-modal="false"
      :close-on-press-escape="!busy"
      :show-close="!busy"
      @close="close"
      ><div class="format-list">
        <el-tag v-for="f in mapFormats" :key="f" type="info">{{ f }}</el-tag>
      </div>
      <p class="muted">
        每行一条；节点可用名称、ID 或地址；简写自动生成接口与子网。
      </p>
      <el-input
        v-model="importText"
        :disabled="busy"
        type="textarea"
        :rows="8"
        placeholder="101,线路A,user101,pass101"
        aria-label="批量导入映射"
      /><el-alert
        v-if="importErrors.length"
        class="import-errors"
        type="error"
        title="以下行未导入"
        :closable="false"
        ><ul>
          <li v-for="(err, i) in importErrors" :key="i">{{ err }}</li>
        </ul></el-alert
      ><template #footer
        ><el-button :disabled="busy" @click="close">取消</el-button
        ><el-button
          type="primary"
          :loading="busy"
          :disabled="!importText.trim()"
          @click="doImport"
          >导入</el-button
        ></template
      ></el-dialog
    >
  </section>
</template>

<style scoped>.mapping-advanced>summary{cursor:pointer;min-height:44px;line-height:44px;font-size:14px}.mapping-advanced>.form-grid{margin-top:8px}</style>

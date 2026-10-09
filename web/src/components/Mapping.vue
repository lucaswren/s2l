<script setup>
import { computed, reactive, ref, watch } from "vue";
import { ElMessageBox } from "element-plus";
import MappingActions from "./MappingActions.vue";
import { api } from "../api/client";

const props = defineProps({
  nodes: { type: Array, default: () => [] },
  mappings: { type: Array, default: () => [] },
  allMappings: { type: Array, default: () => [] },
});
const emit = defineEmits(["changed", "toast"]);

const mode = ref(""); // '' | edit | import
const editingId = ref("");
const busy = ref(false);
const formRef = ref();
const required = { required: true, message: "请填写此项", trigger: "blur" };
const actionId = ref("");
const form = reactive({
  node_id: "",
  tun_name: "tun101",
  table_id: 101,
  subnet: "10.0.101.0/24",
  l2tp_user: "",
  l2tp_pass: "",
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
  while (used.has(id) && id <= 255) id++;
  if (id <= 255) return id;
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
  editingId.value = "";
  const id = nextTable.value;
  Object.assign(form, {
    node_id: props.nodes[0]?.id || "",
    table_id: id,
    tun_name: `tun${id}`,
    subnet: `10.0.${id % 256}.0/24`,
    l2tp_user: `l2tp${id}`,
    l2tp_pass: "",
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
  editingId.value = m.id;
  Object.assign(form, {
    node_id: m.node_id,
    tun_name: m.tun_name,
    table_id: m.table_id,
    subnet: m.subnet,
    l2tp_user: m.l2tp_user || "",
    l2tp_pass: m.l2tp_pass || "",
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
  editingId.value = "";
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
    await api.saveMapping(body);
    emit("toast", {
      type: "ok",
      message: editingId.value ? "映射已更新" : "映射已创建",
    });
    close();
    emit("changed");
  } catch (e) {
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

const editingRunning = computed(() => {
  if (!editingId.value) return false;
  const m = props.allMappings.find((x) => x.id === editingId.value);
  return m?.status === "running";
});
</script>
<template>
  <section>
    <div class="section-head">
      <div>
        <h2>隧道映射</h2>
        <p class="muted">配置出口节点、路由子网与 L2TP 接入凭据</p>
      </div>
      <div class="actions">
        <el-button :disabled="!nodes.length" @click="openImport"
          >批量导入</el-button
        ><el-button type="primary" :disabled="!nodes.length" @click="openCreate"
          >新建映射</el-button
        >
      </div>
    </div>
    <el-alert
      v-if="!nodes.length"
      title="请先在下方添加或导入 SOCKS5 节点。"
      type="info"
      show-icon
      :closable="false"
      class="section-gap"
    />
    <el-table :data="mappings" class="desktop-list" empty-text="暂无映射"
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
                  ? "启动失败"
                  : "已停止"
            }}</el-tag
          >
          <div v-if="row.error_msg" class="error-text">
            {{ row.error_msg }}
          </div></template
        ></el-table-column
      ><el-table-column label="操作" width="220" fixed="right"
        ><template #default="{ row }"
          ><MappingActions
            :mapping="row"
            :action-id="actionId"
            @edit="openEdit"
            @action="action"
            @remove="remove" /></template></el-table-column
    ></el-table>
    <div class="mobile-list">
      <el-empty v-if="!mappings.length" description="暂无映射" /><el-card
        v-for="m in mappings"
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
                  ? "启动失败"
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
          :action-id="actionId"
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
        v-if="editingRunning"
        title="停止映射后才能修改设置与凭据。"
        type="warning"
        :closable="false"
        class="section-gap"
      /><el-form
        ref="formRef"
        :model="form"
        label-position="top"
        :disabled="busy"
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
              :disabled="editingRunning"
              ><el-option
                v-for="n in nodes"
                :key="n.id"
                :label="n.name + ' (' + n.addr + ')'"
                :value="n.id" /></el-select></el-form-item
          ><el-form-item
            label="路由表编号（1–255）"
            prop="table_id"
            :rules="required"
            ><el-input-number
              v-model="form.table_id"
              :min="1"
              :max="255"
              :disabled="editingRunning" /></el-form-item
          ><el-form-item label="TUN 接口" prop="tun_name" :rules="required"
            ><el-input
              v-model="form.tun_name"
              :disabled="editingRunning" /></el-form-item
          ><el-form-item
            label="IPv4 子网"
            prop="subnet"
            :rules="required"
            class="form-wide"
            ><el-input
              v-model="form.subnet"
              :disabled="editingRunning"
              placeholder="10.0.101.0/24" /></el-form-item
          ><el-form-item label="L2TP 账号" prop="l2tp_user" :rules="required"
            ><el-input
              v-model="form.l2tp_user"
              :disabled="editingRunning" /></el-form-item
          ><el-form-item label="L2TP 密码" prop="l2tp_pass" :rules="required"
            ><el-input
              v-model="form.l2tp_pass"
              type="password"
              show-password
              :readonly="editingRunning"
              autocomplete="new-password"
          /></el-form-item></div></el-form
      ><template #footer
        ><el-button v-if="!editingId" :disabled="busy" @click="applyPreset"
          >按路由表填充</el-button
        ><el-button :disabled="busy" @click="close">关闭</el-button
        ><el-button
          type="primary"
          :loading="busy"
          :disabled="editingRunning"
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

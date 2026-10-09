<script setup>
import { reactive, ref } from "vue";
import { ElMessageBox } from "element-plus";
import NodeActions from "./NodeActions.vue";
import SpeedResult from "./SpeedResult.vue";
import { api } from "../api/client";

defineProps({ nodes: { type: Array, default: () => [] } });
const emit = defineEmits(["changed", "toast"]);

const mode = ref(""); // '' | edit | import
const editingId = ref("");
const busy = ref(false);
const formRef = ref();
const required = { required: true, message: "请填写此项", trigger: "blur" };
const testingId = ref("");
const speedResults = reactive({});
const form = reactive({ name: "", addr: "", username: "", password: "" });
const importText = ref("");
const importErrors = ref([]);

const formats = [
  "host:port:user:pass",
  "user:pass@host:port",
  "socks5://user:pass@host:port",
  "name,host:port,user,pass",
];

function directionResult(result, direction) {
  if (result[direction]) return result[direction];
  if (direction === "download") return result;
  return { ok: false, error: result.error || "后端未返回上传结果" };
}

function formatTransfer(result) {
  return result.ok
    ? `${Number(result.speed_mbps || 0).toFixed(1)} Mbps`
    : "失败";
}

function formatSpeed(result) {
  return `下载 ${formatTransfer(directionResult(result, "download"))} / 上传 ${formatTransfer(directionResult(result, "upload"))}`;
}

function resetForm() {
  editingId.value = "";
  Object.assign(form, { name: "", addr: "", username: "", password: "" });
}

function openCreate() {
  resetForm();
  mode.value = "edit";
}

async function openEdit(n) {
  try {
    n = await api.getNode(n.id);
  } catch (e) {
    emit("toast", { type: "err", message: e.message });
    return;
  }
  editingId.value = n.id;
  Object.assign(form, {
    name: n.name || "",
    addr: n.addr || "",
    username: n.username || "",
    password: n.password || "",
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
  resetForm();
}

async function save() {
  if (!(await formRef.value.validate().catch(() => false))) return;
  busy.value = true;
  try {
    const body = { ...form };
    if (editingId.value) body.id = editingId.value;
    await api.saveNode(body);
    emit("toast", {
      type: "ok",
      message: editingId.value ? "已更新" : "已创建",
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
    const res = await api.importNodes({ text: importText.value });
    importErrors.value = res.errors || [];
    const msg =
      `导入 ${res.created || 0} 新 / ${res.updated || 0} 更` +
      (res.skipped ? ` / ${res.skipped} 跳过` : "");
    emit("toast", {
      type: res.errors?.length ? "err" : "ok",
      message: res.errors?.length
        ? `${msg}；${res.errors.slice(0, 2).join("；")}`
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

async function remove(id) {
  try {
    await ElMessageBox.confirm("删除该节点？关联映射需先删除。", "删除节点", {
      type: "warning",
      confirmButtonText: "删除",
      cancelButtonText: "取消",
    });
  } catch {
    return;
  }
  try {
    await api.deleteNode(id);
    delete speedResults[id];
    emit("toast", { type: "ok", message: "已删除" });
    emit("changed");
  } catch (e) {
    emit("toast", { type: "err", message: e.message });
  }
}

async function speedTest(n) {
  testingId.value = n.id;
  try {
    const res = await api.speedTestNode(n.id);
    speedResults[n.id] = res;
    emit("toast", {
      type: res.ok ? "ok" : "err",
      message: `${n.name}: ${formatSpeed(res)}`,
    });
  } catch (e) {
    speedResults[n.id] = { ok: false, error: e.message };
    emit("toast", { type: "err", message: e.message });
  } finally {
    testingId.value = "";
  }
}
</script>
<template>
  <section>
    <div class="section-head">
      <div>
        <h2>SOCKS5 节点</h2>
        <p class="muted">上游出口与上下行测速</p>
      </div>
      <div class="actions">
        <el-button @click="openImport">批量导入</el-button
        ><el-button type="primary" @click="openCreate">新增节点</el-button>
      </div>
    </div>
    <el-table
      :data="nodes"
      class="desktop-list"
      empty-text="暂无节点，请新增或导入"
      ><el-table-column
        prop="name"
        label="名称"
        min-width="130" /><el-table-column
        prop="addr"
        label="地址"
        min-width="170" /><el-table-column
        prop="username"
        label="用户名"
        min-width="100" /><el-table-column label="上下行速度" min-width="210"
        ><template #default="{ row }"
          ><SpeedResult
            :result="speedResults[row.id]" /></template></el-table-column
      ><el-table-column label="操作" width="300" fixed="right"
        ><template #default="{ row }"
          ><NodeActions
            :node="row"
            :testing-id="testingId"
            @speed="speedTest"
            @edit="openEdit"
            @remove="remove" /></template></el-table-column
    ></el-table>
    <div class="mobile-list">
      <el-empty v-if="!nodes.length" description="暂无节点" /><el-card
        v-for="n in nodes"
        :key="n.id"
        shadow="never"
        ><div class="item-title">{{ n.name }}</div>
        <div class="item-details">
          <span>地址</span><span class="mono">{{ n.addr }}</span
          ><span>用户名</span><span>{{ n.username || "—" }}</span
          ><span>测速</span><SpeedResult :result="speedResults[n.id]" />
        </div>
        <NodeActions
          :node="n"
          :testing-id="testingId"
          @speed="speedTest"
          @edit="openEdit"
          @remove="remove"
      /></el-card>
    </div>
    <el-dialog
      :model-value="mode === 'edit'"
      :title="editingId ? '编辑节点' : '新增节点'"
      width="620px"
      class="edit-dialog"
      :close-on-click-modal="false"
      :close-on-press-escape="!busy"
      :show-close="!busy"
      @close="close"
      ><el-form
        ref="formRef"
        :model="form"
        label-position="top"
        :disabled="busy"
        @submit.prevent="save"
        ><div class="form-grid">
          <el-form-item label="名称"
            ><el-input
              v-model="form.name"
              placeholder="留空使用地址" /></el-form-item
          ><el-form-item label="SOCKS5 地址" prop="addr" :rules="required"
            ><el-input
              v-model="form.addr"
              placeholder="host:port" /></el-form-item
          ><el-form-item label="用户名"
            ><el-input
              v-model="form.username"
              autocomplete="off" /></el-form-item
          ><el-form-item label="密码"
            ><el-input
              v-model="form.password"
              type="password"
              show-password
              autocomplete="new-password"
          /></el-form-item></div></el-form
      ><template #footer
        ><el-button :disabled="busy" @click="close">取消</el-button
        ><el-button type="primary" :loading="busy" @click="save"
          >保存</el-button
        ></template
      ></el-dialog
    >
    <el-dialog
      :model-value="mode === 'import'"
      title="批量导入节点"
      width="680px"
      class="edit-dialog"
      :close-on-click-modal="false"
      :close-on-press-escape="!busy"
      :show-close="!busy"
      @close="close"
      ><el-alert
        title="每行一条；同名或同地址会更新现有节点。"
        type="info"
        :closable="false"
      />
      <div class="format-list">
        <el-tag v-for="f in formats" :key="f" type="info">{{ f }}</el-tag>
      </div>
      <el-input
        v-model="importText"
        :disabled="busy"
        type="textarea"
        :rows="8"
        placeholder="1.2.3.4:1080:user:pass"
        aria-label="批量导入节点"
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

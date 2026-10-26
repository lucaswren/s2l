<script setup>
import { tr } from '../i18n/index.js';

import { defineAsyncComponent, reactive, ref } from "vue";
import { ElMessageBox } from "element-plus";
const NetworkTest = defineAsyncComponent(() => import("./NetworkTest.vue"));
import NodeActions from "./NodeActions.vue";
import SpeedResult from "./SpeedResult.vue";
import { api } from "../api/client";

defineProps({ nodes: { type: Array, default: () => [] } });
const emit = defineEmits(["changed", "toast"]);

const networkNode = ref(null);
const mode = ref(""); // '' | edit | import
const editingId = ref("");
const editRevision=ref("");
const busy = ref(false);
const formRef = ref();
const required = { required: true, get message() { return tr("请填写此项"); }, trigger: "blur" };
const testingId = ref("");
const speedResults = reactive({});
const form = reactive({ name: "", addr: "", username: "", password: "",group:"" });
const importText = ref("");
const importErrors = ref([]);
const blockedMappings = ref([]);

const formats = [
  "host:port:user:pass",
  "user:pass@host:port",
  "socks5://user:pass@host:port",
  "name,host:port,user,pass",
];

function formatTransfer(result) {
  return result.ok
    ? `${Number(result.speed_mbps || 0).toFixed(1)} Mbps`
    : tr("失败");
}

function formatSpeed(result) {
  return tr("下载 {0} / 上传 {1}", formatTransfer(result.download), formatTransfer(result.upload));
}

function resetForm() {
  blockedMappings.value = [];
  editingId.value = "";editRevision.value="";
  Object.assign(form, { name: "", addr: "", username: "", password: "",group:"" });
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
  editingId.value = n.id;editRevision.value=n._revision||"";
  blockedMappings.value = [];
  Object.assign(form, {
    name: n.name || "",group:n.group||"",
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
    const preview=await api.previewNode(body,editRevision.value);
    blockedMappings.value = preview.blocked || [];
    if (blockedMappings.value.length) return;
    const labels={get group() { return tr("分组"); },get name() { return tr("名称"); },get addr() { return tr("节点地址"); },get username() { return tr("代理账号"); },get password() { return tr("代理密码"); },get node_id() { return tr("出口节点"); },tun_name:'TUN',get table_id() { return tr("路由表"); },get subnet() { return tr("子网"); },get l2tp_user() { return tr("拨号账号"); },get l2tp_pass() { return tr("拨号密码"); },mtu:'MTU',mss:'MSS',get queue_length() { return tr("队列长度"); },get udp_policy() { return tr("UDP 策略"); },get dns_mode() { return tr("DNS 模式"); },get upload_kbps() { return tr("上传限速"); },get download_kbps() { return tr("下载限速"); },get max_connections() { return tr("连接上限"); }};
    await ElMessageBox.confirm(tr("变更：{0}。影响 {1} 条映射。{2}", preview.fields.map(f=>labels[f]||f).join('、')||tr("无"), preview.affected.length, tr(preview.message)),tr("保存配置"),{get confirmButtonText() { return tr("保存"); },get cancelButtonText() { return tr("返回编辑"); }});
    await api.saveNode(body,editRevision.value);
    emit("toast", {
      type: "ok",
      message: editingId.value ? tr("已更新") : tr("已创建"),
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
    const res = await api.importNodes({ text: importText.value });
    importErrors.value = res.errors || [];
    const msg =
      tr("导入 {0} 新 / {1} 更", res.created || 0, res.updated || 0) +
      (res.skipped ? tr(" / {0} 跳过", res.skipped) : "");
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
    await ElMessageBox.confirm(tr("删除该节点？关联映射需先删除。"), tr("删除节点"), {
      type: "warning",
      get confirmButtonText() { return tr("删除"); },
      get cancelButtonText() { return tr("取消"); },
    });
  } catch {
    return;
  }
  try {
    await api.deleteNode(id);
    delete speedResults[id];
    emit("toast", { type: "ok", get message() { return tr("已删除"); } });
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
        <h2>{{ tr("SOCKS5 节点") }}</h2>

      </div>
      <div class="actions">
        <el-button @click="openImport">{{ tr("批量导入") }}</el-button
        ><el-button type="primary" @click="openCreate">{{ tr("新增节点") }}</el-button>
      </div>
    </div>
    <el-table
      :data="nodes"
      class="desktop-list"
      :empty-text='tr("暂无节点，请新增或导入")'
      ><el-table-column
        prop="name"
        :label='tr("名称")'
        min-width="130" /><el-table-column
        prop="addr"
        :label='tr("地址")'
        min-width="170" /><el-table-column
        prop="username"
        :label='tr("用户名")'
        min-width="100" /><el-table-column :label='tr("上下行速度")' min-width="210"
        ><template #default="{ row }"
          ><SpeedResult
            :result="speedResults[row.id]" /></template></el-table-column
      ><el-table-column :label='tr("操作")' min-width="340"
        ><template #default="{ row }"
          ><NodeActions
            :node="row"
            :testing-id="testingId"
            @network="networkNode = $event"
            @speed="speedTest"
            @edit="openEdit"
            @remove="remove" /></template></el-table-column
    ></el-table>
    <div class="mobile-list">
      <el-empty v-if="!nodes.length" :description='tr("暂无节点")' /><el-card
        v-for="n in nodes"
        :key="n.id"
        shadow="never"
        ><div class="item-title">{{ n.name }}</div>
        <div class="item-details">
          <span>{{ tr("地址") }}</span><span class="mono">{{ n.addr }}</span
          ><span>{{ tr("用户名") }}</span><span>{{ n.username || "—" }}</span
          ><span>{{ tr("测速") }}</span><SpeedResult :result="speedResults[n.id]" />
        </div>
        <NodeActions
          :node="n"
          :testing-id="testingId"
          @network="networkNode = $event"
            @speed="speedTest"
          @edit="openEdit"
          @remove="remove"
      /></el-card>
    </div>
    <NetworkTest v-if="networkNode" :node="networkNode" @close="networkNode = null" @changed="emit('changed')" />
    <el-dialog
      :model-value="mode === 'edit'"
      :title="editingId ? tr(&quot;编辑节点&quot;) : tr(&quot;新增节点&quot;)"
      width="620px"
      class="edit-dialog"
      :close-on-click-modal="false"
      :close-on-press-escape="!busy"
      :show-close="!busy"
      @close="close"
      ><el-alert v-if="blockedMappings.length" type="warning" :closable="false"
        :title='tr("请先停止以下映射，再保存连接参数")'>
        <p>{{ tr("修改代理地址、账号或密码会影响现有连接。请在映射页面停止以下映射后回来保存，再按需启动；名称和分组可直接修改。") }}</p>
        <ul><li v-for="mapping in blockedMappings" :key="mapping.id">{{ mapping.name || mapping.id }} ({{ mapping.id }})</li></ul>
      </el-alert><el-form
        ref="formRef"
        :model="form"
        label-position="top"
        :disabled="busy"
        @submit.prevent="save"
        ><div class="form-grid">
          <el-form-item :label='tr("分组")'><el-input v-model="form.group" maxlength="64"/></el-form-item>
 <el-form-item :label='tr("名称")'
            ><el-input
              v-model="form.name"
              :placeholder='tr("留空使用地址")' /></el-form-item
          ><el-form-item :label='tr("SOCKS5 地址")' prop="addr" :rules="required"
            ><el-input
              v-model="form.addr"
              placeholder="host:port" /></el-form-item
          ><el-form-item :label='tr("用户名")'
            ><el-input
              v-model="form.username"
              autocomplete="off" /></el-form-item
          ><el-form-item :label='tr("密码")'
            ><el-input
              v-model="form.password"
              type="password"
              show-password
              autocomplete="new-password"
          /></el-form-item></div></el-form
      ><template #footer
        ><el-button :disabled="busy" @click="close">{{ tr("取消") }}</el-button
        ><el-button type="primary" :loading="busy" @click="save"
          >{{ tr("保存") }}</el-button
        ></template
      ></el-dialog
    >
    <el-dialog
      :model-value="mode === 'import'"
      :title='tr("批量导入节点")'
      width="680px"
      class="edit-dialog"
      :close-on-click-modal="false"
      :close-on-press-escape="!busy"
      :show-close="!busy"
      @close="close"
      ><el-alert
        :title='tr("每行一条；同名或同地址会更新现有节点。")'
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
        :aria-label='tr("批量导入节点")'
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

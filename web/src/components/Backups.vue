<script setup>
import { onMounted, ref } from "vue";
import { ElMessageBox } from "element-plus";
import { api } from "../api/client";
const emit = defineEmits(["changed"]);
const items = ref([]);
const busy = ref(false);
const error = ref("");
const preview = ref(null);
const selected = ref("");
const message = ref("");
const labels = {add:"新增",remove:"删除",update:"修改（可能包含凭据）"};
async function load() {
  items.value = await api.backups();
}
async function perform(fn) {
  if (busy.value) return;
  busy.value = true; error.value = "";
  try { await fn(); } catch(e) { error.value = e.message; }
  finally { busy.value = false; }
}
async function inspect(id) {
  await perform(async () => { preview.value = null; selected.value = id; preview.value = await api.previewBackup(id); });
}
async function restore() {
  if (!preview.value || busy.value) return;
  const body = {id:selected.value,revision:preview.value.revision,backup_revision:preview.value.backup_revision};
  try {
    await ElMessageBox.confirm("按预览替换全部节点和映射配置？回滚前会自动备份当前配置，恢复后所有映射保持停止。", "恢复配置", {type:"warning",confirmButtonText:"恢复",cancelButtonText:"取消"});
  } catch { return; }
  await perform(async () => {
    const result = await api.restoreBackup(body);
    message.value = result.message; preview.value = null;
    emit("changed"); await load();
  });
}
onMounted(() => perform(load));
</script>
<template>
  <el-card shadow="never" class="section-gap">
    <template #header>配置备份与回滚</template>
    <p>节点或映射配置修改前自动备份，保留最近 10 份；运行状态变化不产生备份。备份包含凭据，仅在服务器私有目录保存，不进入日志包。</p>
    <p>范围：节点与映射。管理账号、监听端口、SSH、IPsec 和系统文件不在此回滚范围。恢复前请停止全部映射，包括操作失败的映射。</p>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <el-alert v-if="message" :title="message" type="success" :closable="false" />
    <div class="actions section-gap">
      <el-button :loading="busy" @click="perform(async () => { await api.createBackup(); await load(); message = '备份已创建'; })">立即备份</el-button>
      <el-button :disabled="busy" @click="perform(load)">刷新备份</el-button>
    </div>
    <el-table :data="items" empty-text="暂无备份">
      <el-table-column label="时间" min-width="180"><template #default="{row}">{{ new Date(row.created_at).toLocaleString() }}</template></el-table-column>
      <el-table-column prop="nodes" label="节点" />
      <el-table-column prop="mappings" label="映射" />
      <el-table-column label="操作"><template #default="{row}"><el-button :disabled="busy" @click="inspect(row.id)">预览回滚</el-button></template></el-table-column>
    </el-table>
    <template v-if="preview">
      <h4>回滚差异</h4>
      <el-alert v-if="!preview.can_restore" title="请先停止全部映射，再重新预览。" type="warning" :closable="false" />
      <el-table :data="preview.changes" empty-text="配置没有差异（运行状态除外）">
        <el-table-column type="expand"><template #default="{row}">
          <p v-if="row.credentials_changed">凭据有变化，密码内容已隐藏。</p>
          <div style="display:flex;gap:16px;flex-wrap:wrap;padding:12px">
            <div><strong>当前配置</strong><pre style="white-space:pre-wrap;overflow-wrap:anywhere">{{ JSON.stringify(row.before || {}, null, 2) }}</pre></div>
            <div><strong>回滚后配置</strong><pre style="white-space:pre-wrap;overflow-wrap:anywhere">{{ JSON.stringify(row.after || {}, null, 2) }}</pre></div>
          </div>
        </template></el-table-column>
        <el-table-column label="类型"><template #default="{row}">{{ row.kind === 'node' ? '节点' : '映射' }}</template></el-table-column>
        <el-table-column prop="id" label="ID" min-width="160" />
        <el-table-column label="变化"><template #default="{row}">{{ labels[row.change] }}</template></el-table-column>
      </el-table>
      <el-button type="warning" :disabled="busy || !preview.can_restore" @click="restore">确认恢复此配置</el-button>
      <el-button :disabled="busy" @click="preview = null">关闭预览</el-button>
    </template>
  </el-card>
</template>

<script setup>
import { tr, dateTime } from '../i18n/index.js';

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
const labels = {get add() { return tr("新增"); },get remove() { return tr("删除"); },get update() { return tr("修改（可能包含凭据）"); }};
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
async function cleanup(ids) {
 if(busy.value || !ids.length)return;
 try { await ElMessageBox.confirm(tr('删除所选 {0} 份备份？此操作无法撤销，当前配置保持不变。',ids.length),tr('清理备份'),{type:'warning',confirmButtonText:tr('删除'),cancelButtonText:tr('取消')}); } catch { return; }
 await perform(async()=>{ await api.deleteBackups(ids); if(ids.includes(selected.value)){preview.value=null;selected.value='';} await load(); message.value=tr('备份已清理'); });
}
async function restore() {
  if (!preview.value || busy.value) return;
  const body = {id:selected.value,revision:preview.value.revision,backup_revision:preview.value.backup_revision};
  try {
    await ElMessageBox.confirm(tr("按预览替换全部节点和映射配置？回滚前会自动备份当前配置，恢复后所有映射保持停止。"), tr("恢复配置"), {type:"warning",get confirmButtonText() { return tr("恢复"); },get cancelButtonText() { return tr("取消"); }});
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
  <el-card shadow="never" class="settings-card">
    <template #header>{{ tr("配置备份与回滚") }}</template>
    <p>{{ tr("节点与映射修改前自动备份，保留最近 10 份。恢复前需停止全部映射。") }}</p>
    <details class="muted"><summary>{{ tr("备份范围") }}</summary><p>{{ tr("包含节点、映射及其凭据；不含管理账号、端口、SSH、IPsec 和系统时间。备份仅存于服务器，不进入日志包。") }}</p></details>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <el-alert v-if="message" :title="tr(message)" type="success" :closable="false" />
    <div class="actions section-gap">
      <el-button :loading="busy" @click="perform(async () => { await api.createBackup(); await load(); message = tr(&quot;备份已创建&quot;); })">{{ tr("立即备份") }}</el-button>
      <el-button :disabled="busy" @click="perform(load)">{{ tr("刷新备份") }}</el-button>
      <el-button type="danger" plain :disabled="busy || items.length<2" @click="cleanup(items.slice(1).map(row=>row.id))">{{tr('清理旧备份')}}</el-button>
    </div>
    <p class="muted">{{tr("清理旧备份会保留最新一份配置备份。")}}</p>
    <el-table class="desktop-list" :data="items" :empty-text='tr("暂无备份")'>
      <el-table-column :label='tr("时间")' min-width="180"><template #default="{row}">{{ dateTime(row.created_at) }}</template></el-table-column>
      <el-table-column prop="nodes" :label='tr("节点")' />
      <el-table-column prop="mappings" :label='tr("映射")' />
      <el-table-column :label='tr("操作")' min-width="210"><template #default="{row}"><div class="actions"><el-button :disabled="busy" @click="inspect(row.id)">{{ tr("预览回滚") }}</el-button><el-button type="danger" plain :disabled="busy" @click="cleanup([row.id])">{{tr('删除')}}</el-button></div></template></el-table-column>
    </el-table>
    <div class="mobile-list backup-mobile"><el-empty v-if="!items.length" :description='tr("暂无备份")' /><article v-for="row in items" :key="row.id" class="backup-item"><div><strong>{{ dateTime(row.created_at) }}</strong><p class="muted">{{ tr("节点") }} {{ row.nodes }} {{ tr("· 映射") }} {{ row.mappings }}</p></div><div class="actions"><el-button :disabled="busy" @click="inspect(row.id)">{{ tr("预览") }}</el-button><el-button type="danger" plain :disabled="busy" @click="cleanup([row.id])">{{tr("删除")}}</el-button></div></article></div>
    <template v-if="preview">
      <h4>{{ tr("回滚差异") }}</h4>
      <el-alert v-if="!preview.can_restore" :title='tr("请先停止全部映射，再重新预览。")' type="warning" :closable="false" />
      <el-table class="desktop-list" :data="preview.changes" :empty-text='tr("配置没有差异（运行状态除外）")'>
        <el-table-column type="expand"><template #default="{row}">
          <p v-if="row.credentials_changed">{{ tr("凭据有变化，密码内容已隐藏。") }}</p>
          <div style="display:flex;gap:16px;flex-wrap:wrap;padding:12px">
            <div><strong>{{ tr("当前配置") }}</strong><pre style="white-space:pre-wrap;overflow-wrap:anywhere">{{ JSON.stringify(row.before || {}, null, 2) }}</pre></div>
            <div><strong>{{ tr("回滚后配置") }}</strong><pre style="white-space:pre-wrap;overflow-wrap:anywhere">{{ JSON.stringify(row.after || {}, null, 2) }}</pre></div>
          </div>
        </template></el-table-column>
        <el-table-column :label='tr("类型")'><template #default="{row}">{{ row.kind === 'node' ? tr("节点") : tr("映射") }}</template></el-table-column>
        <el-table-column prop="id" label="ID" min-width="160" />
        <el-table-column :label='tr("变化")'><template #default="{row}">{{ labels[row.change] }}</template></el-table-column>
      </el-table>
      <div class="mobile-list backup-mobile"><el-empty v-if="!preview.changes.length" :description='tr("配置没有差异")' /><details v-for="row in preview.changes" :key="row.kind + '/' + row.id"><summary>{{ row.kind === 'node' ? tr("节点") : tr("映射") }} · {{ labels[row.change] }} · {{ row.id }}</summary><p v-if="row.credentials_changed">{{ tr("凭据有变化，密码已隐藏。") }}</p><strong>{{ tr("当前配置") }}</strong><pre>{{ JSON.stringify(row.before || {}, null, 2) }}</pre><strong>{{ tr("回滚后配置") }}</strong><pre>{{ JSON.stringify(row.after || {}, null, 2) }}</pre></details></div>
      <div class="actions">
      <el-button type="warning" :disabled="busy || !preview.can_restore" @click="restore">{{ tr("确认恢复此配置") }}</el-button>
      <el-button :disabled="busy" @click="preview = null">{{ tr("关闭预览") }}</el-button>
      </div>
    </template>
  </el-card>
</template>

<style scoped>
.backup-mobile{margin:12px 0;}
.backup-item{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:12px 0;border-bottom:1px solid var(--el-border-color-lighter);}
.backup-item p{margin:6px 0 0;}
.backup-mobile details{min-width:0;overflow-wrap:anywhere;}
.backup-mobile summary{cursor:pointer;font-size:14px;line-height:1.7;}
.backup-mobile pre{max-width:100%;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;font-size:14px;background:var(--el-fill-color-light);padding:12px;border-radius:6px;}
</style>

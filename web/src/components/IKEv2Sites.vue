<script setup>
import { onMounted, reactive, ref } from "vue";
import { api } from "../api/client";
import { tr } from "../i18n/index.js";

const draft = reactive({ enabled: false, gateway: "", local_subnet: "", sites: [], current_password: "" });
const mappings = ref([]);
const loaded = ref(false);
const busy = ref(false);
const message = ref("");
const error = ref("");
const active = ref(false);
const pending = ref(false);

function populate(result) {
  Object.assign(draft, {
    enabled: result.enabled, gateway: result.gateway || "", local_subnet: result.local_subnet || "",
    sites: result.sites.map(site => ({ egress: "socks5", ...site, psk: "" })), current_password: "",
  });
  active.value = result.active;
  pending.value = result.pending;
}

onMounted(async () => {
  try {
    const [result, rows] = await Promise.all([api.ikev2Sites(), api.listMappings()]);
    populate(result);
    mappings.value = rows;
    loaded.value = true;
  } catch (err) {
    error.value = err.message;
  }
});

function addSite() {
  const used = new Set(draft.sites.map(site => site.reqid));
  let reqid = 101;
  while (used.has(reqid)) reqid++;
  draft.sites.push({ name: "", gateway: "", subnet: "", reqid, egress: "socks5", mapping_id: "", wan_interface: "", psk: "" });
}

async function save() {
  busy.value = true;
  error.value = message.value = "";
  try {
    populate(await api.saveIKEv2Sites(draft));
    message.value = tr("已保存");
  } catch (err) {
    error.value = err.message;
  } finally {
    busy.value = false;
    draft.current_password = "";
  }
}
</script>

<template>
  <el-card>
    <template #header>IKEv2 Site-to-Site IPsec</template>
    <p>{{ tr("站点设置保存后，在服务器重启并恢复映射后生效。") }}</p>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <el-alert v-if="message" :title="message" type="success" :closable="false" />
    <el-form v-if="loaded" label-position="top" @submit.prevent="save">
      <p v-if="active">{{ tr("已启用，服务活动") }}</p>
      <p v-if="pending">{{ tr("等待维护窗口重启") }}</p>
      <el-form-item :label="tr('启用')"><el-switch v-model="draft.enabled" /></el-form-item>
      <template v-if="draft.enabled">
        <el-form-item :label="tr('本端网关 IPv4')"><el-input v-model.trim="draft.gateway" /></el-form-item>
        <el-form-item :label="tr('受保护的目标网段')"><el-input v-model.trim="draft.local_subnet" placeholder="198.51.100.0/24" /></el-form-item>
        <el-card v-for="(site, index) in draft.sites" :key="index" shadow="never">
          <el-form-item :label="tr('站点名称')"><el-input v-model.trim="site.name" placeholder="sg01" /></el-form-item>
          <el-form-item :label="tr('对端网关 IPv4')"><el-input v-model.trim="site.gateway" /></el-form-item>
          <el-form-item :label="tr('对端子网')"><el-input v-model.trim="site.subnet" placeholder="10.21.0.0/24" /></el-form-item>
          <el-form-item label="IPsec reqid"><el-input-number v-model="site.reqid" :min="1" :max="16383" /></el-form-item>
          <el-form-item :label="tr('站点出口')">
            <el-select v-model="site.egress">
              <el-option value="socks5" :label="tr('SOCKS5 映射')" />
              <el-option value="wan" :label="tr('本机 WAN')" />
            </el-select>
          </el-form-item>
          <el-form-item v-if="site.egress === 'socks5'" :label="tr('SOCKS5 映射')">
            <el-select v-model="site.mapping_id">
              <el-option v-for="m in mappings" :key="m.id" :value="m.id" :label="`${m.l2tp_user} · ${m.tun_name}`" :disabled="m.status !== 'running'" />
            </el-select>
          </el-form-item>
          <el-form-item v-else :label="tr('WAN 网卡')">
            <el-input v-model.trim="site.wan_interface" placeholder="ens3" />
          </el-form-item>
          <el-form-item label="PSK"><el-input v-model="site.psk" type="password" autocomplete="new-password" :placeholder="tr('已有站点留空保留密钥')" /></el-form-item>
          <el-button @click="draft.sites.splice(index, 1)">{{ tr("删除") }}</el-button>
        </el-card>
        <el-button :disabled="draft.sites.length >= 16" @click="addSite">{{ tr("新增站点") }}</el-button>
      </template>
      <el-form-item :label="tr('当前管理密码')">
        <el-input v-model="draft.current_password" type="password" autocomplete="current-password" />
      </el-form-item>
      <el-button native-type="submit" type="primary" :loading="busy">{{ tr("保存") }}</el-button>
    </el-form>
  </el-card>
</template>

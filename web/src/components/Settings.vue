<script setup>
import PPPManagement from "./PPPManagement.vue";
import { onMounted, reactive, ref } from "vue";
import { ElMessageBox } from "element-plus";
import { api, setCredentials, clearCredentials } from "../api/client";
import { mergeSettingsDraft } from "../utils/settings-draft";
import { generatePassword } from "../utils/password";
import SoftwareUpdate from "./SoftwareUpdate.vue";
import TimeSettings from "./TimeSettings.vue";
import Backups from "./Backups.vue";
import DebugSettings from "./DebugSettings.vue";
defineProps({active:{type:Boolean,default:true}});
const emit = defineEmits(["toast", "changed"]);
const settings = ref({
  account_enabled: false,
  ssh_enabled: false,
  ssh_ports: [],
  ipsec: {
    available: false,
    enabled: false,
    active: false,
    psk_configured: false,
    protected: false,
  },
});
const loading = ref(false);
const busy = ref("");
const error = ref("");
const updateOpen=ref(false),debugOpen=ref(false);
const ipsecRef = ref();
const ipsec = reactive({ enabled: false, psk: "", current_password: "" });
const accountRef = ref();
const sshRef = ref();
const webPortRef = ref();
const webPort = reactive({ port: Number(location.port) || 443 });
const sshPasswordRef = ref();
const sshPassword = reactive({
  password: "",
  confirm_password: "",
});
const httpsConnection = location.protocol === "https:";
const sshPasswordRules = [
  { required: true, message: "请输入新 SSH 密码", trigger: "blur" },
  {
    pattern: /^[\x21-\x7e]{12,128}$/,
    message: "12–128 位可见 ASCII 字符，不支持空格",
    trigger: "blur",
  },
];
const sshConfirmRule = {
  validator: (_, v, cb) =>
    v === sshPassword.password ? cb() : cb(new Error("两次输入的密码不一致")),
  trigger: "blur",
};
const account = reactive({
  username: "",
  password: "",
  confirm: "",
});
const ssh = reactive({ port: 22 });
const required = { required: true, message: "请填写此项", trigger: "blur" };
const usernameRule = [
  required,
  {
    pattern: /^[A-Za-z0-9_.-]{1,64}$/,
    message: "1–64 位字母、数字、点、下划线或连字符",
    trigger: "blur",
  },
];
const passwordRule = {
  validator: (_, v, cb) =>
    !v || /^[A-Za-z0-9_.@+-]{12,128}$/.test(v)
      ? cb()
      : cb(new Error("12–128 位字母、数字或 _ . @ + -")),
  trigger: "blur",
};
const confirmRule = {
  validator: (_, v, cb) =>
    v === account.password ? cb() : cb(new Error("两次输入的密码不一致")),
  trigger: "blur",
};
let loaded = false;
async function load() {
 if (loading.value) return;
 const previous = loaded ? settings.value : null;
  loading.value = true;
  try {
    const next = await api.settings();
    Object.assign(account, mergeSettingsDraft(account, previous && {username:previous.admin_user}, {username:next.admin_user}));
    Object.assign(ssh, mergeSettingsDraft(ssh, previous && {port:previous.ssh_ports?.[0] || 22}, {port:next.ssh_ports?.[0] || 22}));
    Object.assign(webPort, mergeSettingsDraft(webPort, previous && {port:previous.web_port || 443}, {port:next.web_port || 443}));
    Object.assign(ipsec, mergeSettingsDraft(ipsec, previous && {enabled:!!previous.ipsec?.enabled}, {enabled:!!next.ipsec?.enabled}));
    settings.value = next;
    loaded = true;
    error.value = "";
  } catch (e) {
    error.value = e.message;
  } finally {
    loading.value = false;
  }
}
async function saveAccount() {
  if (!(await accountRef.value.validate().catch(() => false))) return;
  busy.value = "account";
  try {
    const res = await api.saveAccount({
      username: account.username,
      password: account.password,
    });
    const renamed = account.username !== settings.value.admin_user;
    const passwordChanged = !!account.password;
    if (account.password) setCredentials(account.username, account.password);
    account.password = account.confirm = "";

    emit("toast", {
      type: "ok",
      message: res.message + "；重新打开网页时使用新账号登录。",
    });
    if (renamed && !passwordChanged) {
      clearCredentials();
      window.location.reload();
      return;
    }
    await load();
  } catch (e) {
    emit("toast", { type: "err", message: e.message });
  } finally {
    busy.value = "";
  }
}
async function saveWebPort() {
  if (!(await webPortRef.value.validate().catch(() => false))) return;
  try {
    await ElMessageBox.confirm(
      `将 HTTPS 端口改为 ${webPort.port}。请先放行服务器防火墙和云安全组中的 TCP ${webPort.port}。修改后使用新地址重新登录。`,
      "修改 HTTPS 端口",
      {
        type: "warning",
        confirmButtonText: "修改端口",
        cancelButtonText: "取消",
      },
    );
  } catch {
    return;
  }
  busy.value = "web-port";
  try {
    const res = await api.saveWebPort({ ...webPort });
    settings.value.public_url = res.public_url;
    settings.value.web_port = res.port;
    emit("toast", { type: "ok", message: res.message + "：" + res.public_url });
  } catch (e) {
    emit("toast", { type: "err", message: e.message });
  } finally {
    busy.value = "";
  }
}
async function saveSSH() {
  if (!(await sshRef.value.validate().catch(() => false))) return;
  try {
    await ElMessageBox.confirm(
      `将 SSH 端口改为 ${ssh.port}。请先放行服务器防火墙和云安全组中的 TCP ${ssh.port}，并保留当前 SSH 连接，修改后使用新端口验证登录。`,
      "修改 SSH 端口",
      {
        type: "warning",
        confirmButtonText: "修改端口",
        cancelButtonText: "取消",
      },
    );
  } catch {
    return;
  }
  busy.value = "ssh";
  try {
    const res = await api.saveSSH({ ...ssh });
    emit("toast", { type: "ok", message: res.message });
    await load();
  } catch (e) {
    emit("toast", { type: "err", message: e.message });
  } finally {
    busy.value = "";
  }
}
async function saveIPsec() {
  if (!(await ipsecRef.value.validate().catch(() => false))) return;
  if (ipsec.enabled && !ipsec.psk && !settings.value.ipsec?.psk_configured) {
    emit("toast", { type: "err", message: "启用 IPsec 前请设置预共享密钥" });
    return;
  }
  try {
    await ElMessageBox.confirm(
      ipsec.enabled
        ? "启用后仅接受通过 IPsec 加密的 L2TP 连接。请放行 UDP 500、4500 和 ESP 协议，客户端填写相同的预共享密钥。现有普通 L2TP 连接需要重新拨号。"
        : "关闭后恢复普通 L2TP，客户端需清空预共享密钥并重新拨号。",
      "应用接入设置",
      {
        type: "warning",
        confirmButtonText: "应用设置",
        cancelButtonText: "取消",
      },
    );
  } catch {
    return;
  }
  busy.value = "ipsec";
  try {
    const res = await api.saveIPsec({ ...ipsec });
    ipsec.psk = ipsec.current_password = "";
    emit("toast", { type: "ok", message: res.message });
    await load();
  } catch (e) {
    emit("toast", { type: "err", message: e.message });
    await load();
  } finally {
    busy.value = "";
  }
}
function generateSSHPassword() {
  try {
    sshPassword.password = sshPassword.confirm_password = generatePassword();
    sshPasswordRef.value?.clearValidate(["password", "confirm_password"]);
    emit("toast", {
      type: "ok",
      message: "已生成 16 位随机密码，请查看并妥善记录后提交",
    });
  } catch (e) {
    emit("toast", { type: "err", message: e.message });
  }
}
async function saveSSHPassword() {
  if (!(await sshPasswordRef.value.validate().catch(() => false))) return;
  try {
    await ElMessageBox.confirm(
      "将修改 root 的 Linux 登录密码。请保留当前 SSH 连接，并在另一终端使用新密码验证登录；网页管理密码和 SSH 密钥不受影响。",
      "修改 SSH 登录密码",
      {
        type: "warning",
        confirmButtonText: "修改密码",
        cancelButtonText: "取消",
      },
    );
  } catch {
    return;
  }
  busy.value = "ssh-password";
  try {
    const res = await api.saveSSHPassword({ ...sshPassword });
    sshPassword.password = sshPassword.confirm_password = "";
    emit("toast", { type: "ok", message: res.message });
  } catch (e) {
    emit("toast", { type: "err", message: e.message });
  } finally {
    busy.value = "";
  }
}
onMounted(load);
</script>
<template>
 <section class="system-settings">
  <div class="section-head">

   <el-button :loading="loading" :disabled="!!busy" @click="load">刷新设置</el-button>
  </div>
  <el-alert v-if="error" :title="error" type="error" :closable="false" class="section-gap" />
  <div class="settings-stack">
   <details class="settings-group settings-disclosure" aria-label="接入与账号"><summary>接入与账号<span>L2TP、IPsec、管理员</span></summary>
    <div class="settings-grid"><el-card shadow="never" class="settings-card">
    <template #header
      ><div class="card-heading">
        <span>L2TP 接入</span
        ><el-tag
          :type="
            settings.ipsec?.enabled
              ? settings.ipsec?.active && settings.ipsec?.protected
                ? 'success'
                : 'danger'
              : 'info'
          "
          >{{
            settings.ipsec?.enabled
              ? settings.ipsec?.active && settings.ipsec?.protected
                ? "IPsec 已启用"
                : "IPsec 状态异常"
              : "普通 L2TP"
          }}</el-tag
        >
      </div></template
    >
    <el-alert
      v-if="!settings.ipsec?.available"
      title="IPsec 组件尚未就绪，请升级服务器安装文件。"
      type="info"
      :closable="false"
      class="section-gap"
    />
    <el-form
      ref="ipsecRef"
      :model="ipsec"
      label-position="top"
      :disabled="
        !!busy ||
        loading ||
        !settings.account_enabled ||
        !settings.ipsec?.available
      "
      @submit.prevent="saveIPsec"
    >
      <el-form-item label="启用 IPsec" class="ipsec-switch"
        ><el-switch v-model="ipsec.enabled" /><span class="switch-caption">{{
          ipsec.enabled
            ? "L2TP/IPsec · IKEv1 · 预共享密钥认证"
            : "L2TP · 不加密接入流量"
        }}</span></el-form-item
      >
      <div class="form-grid">
        <el-form-item label="预共享密钥（PSK）" prop="psk" :rules="passwordRule"
          ><el-input
            v-model="ipsec.psk"
            type="password"
            show-password
            autocomplete="new-password"
            maxlength="128"
            :placeholder="
              settings.ipsec?.psk_configured
                ? '已设置，留空保留现有密钥'
                : '12–128 位，与客户端保持一致'
            " /></el-form-item
        ><el-form-item
          label="当前管理密码"
          prop="current_password"
          :rules="required"
          ><el-input
            v-model="ipsec.current_password"
            type="password"
            show-password
            autocomplete="current-password"
        /></el-form-item>
      </div>
      <p class="muted">
        对全部映射生效；切换接入方式或密钥后，客户端需重新拨号。
      </p>
      <el-button type="primary" :loading="busy === 'ipsec'" native-type="submit"
        >应用接入设置</el-button
      >
    </el-form>
  </el-card><el-card shadow="never" class="settings-card"
        ><template #header>管理账号</template>
        <el-alert
          v-if="!settings.account_enabled"
          title="需指定配置文件并启用管理认证后才能修改。"
          type="info"
          :closable="false"
          class="section-gap"
        />
        <el-form
          ref="accountRef"
          :model="account"
          label-position="top"
          :disabled="!!busy || loading || !settings.account_enabled"
          @submit.prevent="saveAccount"
        >
          <el-form-item label="管理用户名" prop="username" :rules="usernameRule"
            ><el-input
              v-model="account.username"
              autocomplete="username"
              maxlength="64"
          /></el-form-item>

          <el-form-item
            label="新密码（留空保留当前密码）"
            prop="password"
            :rules="passwordRule"
            ><el-input
              v-model="account.password"
              type="password"
              show-password
              autocomplete="new-password"
              placeholder="至少 12 位"
              maxlength="128"
          /></el-form-item>
          <el-form-item label="确认新密码" prop="confirm" :rules="confirmRule"
            ><el-input
              v-model="account.confirm"
              type="password"
              show-password
              autocomplete="new-password"
              maxlength="128"
          /></el-form-item>
          <el-button
            type="primary"
            :loading="busy === 'account'"
            native-type="submit"
            >保存账号</el-button
          >
        </el-form>
      </el-card></div>
   </details>
   <details class="settings-group settings-disclosure" aria-label="远程管理"><summary>远程管理<span>面板、SSH、L2TP 管理访问</span></summary>
 <PPPManagement />
    <div class="settings-grid"><div class="settings-stack"><el-card shadow="never" class="settings-card">
    <template #header>HTTPS 管理端口</template>
    <el-alert
      :title="
        settings.web_port_enabled
          ? '先放行新 TCP 端口；修改后使用新地址登录。'
          : 'HTTPS 管理组件不可用，请更新安装文件。'
      "
      type="info"
      show-icon
      :closable="false"
      class="section-gap"
    />
    <p v-if="settings.public_url" class="muted">
      管理地址：<el-link
        :href="settings.public_url"
        type="primary"
        class="mono"
        >{{ settings.public_url }}</el-link
      >
    </p>
    <el-form
      ref="webPortRef"
      :model="webPort"
      label-position="top"
      :disabled="!!busy || loading || !settings.web_port_enabled"
      @submit.prevent="saveWebPort"
    >
      <div class="form-grid">
        <el-form-item label="HTTPS 端口" prop="port" :rules="required">
          <el-input-number
            v-model="webPort.port"
            :min="1"
            :max="65535"
            :precision="0"
          />
        </el-form-item>
      </div>
      <el-button
        type="primary"
        :loading="busy === 'web-port'"
        native-type="submit"
        >修改 HTTPS 端口</el-button
      >
    </el-form>
  </el-card><el-card shadow="never"
        ><template #header>SSH 端口</template>
        <el-alert
          :title="
            settings.ssh_enabled
              ? '先放行新端口，并保留当前连接验证登录。'
              : 'SSH 管理组件不可用。'
          "
          type="info"
          show-icon
          :closable="false"
          class="section-gap"
        />
        <p class="muted">
          当前配置端口：{{ settings.ssh_ports.join("、") || "未能读取" }}
        </p>
        <el-form
          ref="sshRef"
          :model="ssh"
          label-position="top"
          :disabled="!!busy || loading || !settings.ssh_enabled"
          @submit.prevent="saveSSH"
        >
          <el-form-item label="新 SSH 端口" prop="port" :rules="required"
            ><el-input-number
              v-model="ssh.port"
              :min="1"
              :max="65535"
              :precision="0"
          /></el-form-item>

          <el-button
            type="primary"
            :loading="busy === 'ssh'"
            native-type="submit"
            >修改 SSH 端口</el-button
          >
        </el-form>
      </el-card></div><el-card shadow="never" class="settings-card ssh-password-card">
        <template #header>SSH 登录密码</template>
        <el-alert
          :title="
            !httpsConnection
              ? '请通过 HTTPS 打开管理页面后修改密码。'
              : !settings.ssh_password_enabled
                ? '当前环境不支持修改 Linux 登录密码。'
                : '修改 root 密码；请保留当前连接验证新密码。'
          "
          type="info"
          show-icon
          :closable="false"
          class="section-gap"
        />
        <el-form
          ref="sshPasswordRef"
          :model="sshPassword"
          label-position="top"
          :disabled="
            !!busy ||
            loading ||
            !httpsConnection ||
            !settings.ssh_password_enabled
          "
          @submit.prevent="saveSSHPassword"
        >
          <el-form-item label="Linux 用户"
            ><el-input model-value="root" readonly
          /></el-form-item>
          <el-form-item
            label="新 SSH 登录密码"
            prop="password"
            :rules="sshPasswordRules"
            ><el-input
              v-model="sshPassword.password"
              type="password"
              show-password
              autocomplete="new-password"
              maxlength="128"
              placeholder="至少 12 位"
              ><template #append
                ><el-button @click="generateSSHPassword"
                  >随机生成</el-button
                ></template
              ></el-input
            ></el-form-item
          >
          <el-form-item
            label="确认新 SSH 登录密码"
            prop="confirm_password"
            :rules="sshConfirmRule"
            ><el-input
              v-model="sshPassword.confirm_password"
              type="password"
              show-password
              autocomplete="new-password"
              maxlength="128"
          /></el-form-item>
          <p class="muted">
            仅修改密码；密钥登录策略与账号锁定状态保持不变。
          </p>
          <el-button
            type="primary"
            :loading="busy === 'ssh-password'"
            native-type="submit"
            >修改 SSH 密码</el-button
          >
        </el-form>
      </el-card></div>
   </details>
   <details class="settings-disclosure" aria-label="时间与更新" @toggle="updateOpen=$event.target.open"><summary>时间与更新<span>时区、同步、软件版本</span></summary><div class="settings-grid">
    <TimeSettings />
    <SoftwareUpdate :active="active && updateOpen" />
   </div></details>
   <details class="settings-disclosure" aria-label="备份与排障" @toggle="debugOpen=$event.target.open"><summary>备份与排障<span>备份恢复、调试记录</span></summary><div class="settings-grid">
    <DebugSettings :active="active && debugOpen" />
    <Backups @changed="emit('changed')" />
   </div></details>
  </div>
 </section>
</template>
<style scoped>
.settings-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:20px; align-items:start; }
.settings-stack { display:grid; gap:20px; min-width:0; }
.settings-grid :deep(.el-card) { min-width:0; margin-bottom:0; }
.settings-grid :deep(.form-grid) { grid-template-columns:1fr; }
.settings-group { min-width:0; }.settings-disclosure{border:1px solid var(--el-border-color-light);border-radius:8px;padding:0 16px}.settings-disclosure>summary{cursor:pointer;padding:16px 0;font-weight:600;min-height:24px}.settings-disclosure>summary span{display:block;margin-top:4px;font-size:12px;font-weight:400;color:var(--el-text-color-secondary)}.settings-disclosure[open]{padding-bottom:16px}
@media(max-width:900px) { .settings-grid { grid-template-columns:1fr; } }
</style>

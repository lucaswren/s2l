<script setup>
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref } from 'vue';
const App = defineAsyncComponent(() => import('./App.vue'));
import LanguageSelector from './components/LanguageSelector.vue';
import { api } from './api/client';
import { language, tr, setLanguage, serverLanguage, canPersistLanguage } from './i18n/index.js';
import en from 'element-plus/es/locale/lang/en.mjs';
import zhTW from 'element-plus/es/locale/lang/zh-tw.mjs';
import zhCN from 'element-plus/es/locale/lang/zh-cn.mjs';
const elementLocales = { en, 'zh-TW': zhTW, 'zh-CN': zhCN };
const ready = ref(false), authenticated = ref(false), busy = ref(false);
const authEnabled = ref(false);
const username = ref(''), password = ref(''), error = ref('');
const locale = computed(() => elementLocales[language.value]);
let disposed = false;
function expired() { authenticated.value = false; password.value = ''; }
async function check() {
  if (busy.value) return;
  busy.value = true;
  try {
    const status = await api.authStatus();
    if (!disposed) { serverLanguage.value=status.language || '';canPersistLanguage.value=status.language_available === true;if(serverLanguage.value)setLanguage(serverLanguage.value); authEnabled.value = status.enabled === true; authenticated.value = status.authenticated === true; ready.value = true; error.value = ''; }
  } catch (e) { if (!disposed) error.value = e.message; }
  finally { busy.value = false; }
}
async function login() {
  if (busy.value || !username.value || !password.value) return;
  busy.value = true; error.value = '';
  try {
    await api.login({ username: username.value, password: password.value });
    password.value = '';
    if (!disposed) authenticated.value = true;
  } catch (e) { if (!disposed) error.value = e.message; }
  finally { busy.value = false; }
}
onMounted(() => { window.addEventListener('s2l-auth-required', expired); check(); });
onUnmounted(() => { disposed = true; window.removeEventListener('s2l-auth-required', expired); });
</script>
<template>
  <el-config-provider :locale="locale" size="default">
    <App v-if="ready && authenticated" :auth-enabled="authEnabled" />
    <main v-else class="login-page">
      <section class="login-card">
        <div class="login-heading"><h1>S2L</h1><LanguageSelector /></div>
        <h2>{{ tr('管理登录') }}</h2>
        <el-alert v-if="error" :title="tr(error)" type="error" :closable="false" role="alert" />
        <form v-if="ready" @submit.prevent="login">
          <label for="login-user">{{ tr('用户名') }}</label>
          <el-input id="login-user" v-model="username" name="username" autocomplete="username" required maxlength="256" :disabled="busy" />
          <label for="login-password">{{ tr('密码') }}</label>
          <el-input id="login-password" v-model="password" name="password" type="password" autocomplete="current-password" required maxlength="1024" :disabled="busy" />
          <el-button class="login-submit" type="primary" native-type="submit" :loading="busy">{{ tr('登录') }}</el-button>
        </form>
        <el-button v-else :loading="busy" @click="check">{{ tr('重新加载') }}</el-button>
      </section>
    </main>
  </el-config-provider>
</template>

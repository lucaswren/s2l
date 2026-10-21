<script setup>
import { onMounted, ref } from 'vue';
import { ElMessage } from 'element-plus';
import { language, languages, setLanguage, serverLanguage, canPersistLanguage, tr } from '../i18n/index.js';
import { api } from '../api/client.js';
const props=defineProps({persist:Boolean});
const busy=ref(false);
async function save() {
 if(!props.persist || !canPersistLanguage.value || busy.value || serverLanguage.value===language.value)return;
 busy.value=true;
 try { const result=await api.saveLanguage(language.value);serverLanguage.value=result.language; }
 catch { ElMessage.error(tr('语言设置保存失败，请重试')); }
 finally { busy.value=false; }
}
function change(value) { setLanguage(value);save(); }
onMounted(save);
</script>
<template>
  <select class="language-selector" :value="language" :disabled="busy" aria-label="Language / 語言" title="Language / 語言" @change="change($event.target.value)">
    <option v-for="item in languages" :key="item.value" :value="item.value">{{ item.label }}</option>
  </select>
</template>

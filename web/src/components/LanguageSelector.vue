<script setup>
import { language, languages, setLanguage } from '../i18n/index.js';
</script>
<template>
  <select class="language-selector" :value="language" aria-label="Language / 語言" title="Language / 語言" @change="setLanguage($event.target.value)">
    <option v-for="item in languages" :key="item.value" :value="item.value">{{ item.label }}</option>
  </select>
</template>

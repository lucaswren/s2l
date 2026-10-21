import { ref } from 'vue';
import messages from './messages.json' with { type: 'json' };

export const languages = [
  { value: 'zh-CN', label: '简体中文' },
  { value: 'zh-TW', label: '繁體中文' },
  { value: 'en', label: 'English' },
];
export function normalizeLanguage(value) {
  if (/^zh-(TW|HK|MO|Hant)/i.test(value || '')) return 'zh-TW';
  if (/^en(?:-|$)/i.test(value || '')) return 'en';
  return 'zh-CN';
}
function initialLanguage() {
  try {
    const saved = globalThis.localStorage?.getItem('s2l-language');
    if (languages.some(item => item.value === saved)) return saved;
  } catch { /* Storage can be disabled; language selection still works. */ }
  return normalizeLanguage(globalThis.navigator?.language);
}
export const language = ref(initialLanguage());
export const serverLanguage = ref(''), canPersistLanguage = ref(false);
const htmlLanguages = {en:'en','zh-CN':'zh-Hans','zh-TW':'zh-Hant'};
export function setLanguage(value) {
  language.value = normalizeLanguage(value);
  try { globalThis.localStorage?.setItem('s2l-language', language.value); } catch { /* Optional persistence. */ }
  if (globalThis.document) document.documentElement.lang = htmlLanguages[language.value];
}
// Replaces numbered arguments only once, so user-controlled values are never
// interpreted as markup or additional placeholders.
export function tr(key, ...values) {
  key = typeof key === 'string' ? key : String(key ?? '');
  const entry = messages[key];
  const text = language.value === 'en' ? entry?.en : language.value === 'zh-TW' ? entry?.tw : entry?.cn || key;
  return (text || key).replace(/\{(\d+)\}/g, (match, index) => index < values.length ? String(values[index]) : match);
}
export function dateTime(value) { return new Date(value).toLocaleString(language.value); }
export function clockTime(value) { return new Date(value).toLocaleTimeString(language.value); }
setLanguage(language.value);

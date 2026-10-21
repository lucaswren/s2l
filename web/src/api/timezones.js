import { tr, language } from '../i18n/index.js';
const regions = { get Africa() { return tr("非洲"); }, get America() { return tr("美洲"); }, get Antarctica() { return tr("南极洲"); }, get Arctic() { return tr("北极地区"); }, get Asia() { return tr("亚洲"); }, get Atlantic() { return tr("大西洋"); }, get Australia() { return tr("澳大利亚"); }, get Europe() { return tr("欧洲"); }, get Indian() { return tr("印度洋"); }, get Pacific() { return tr("太平洋"); }, get Etc() { return tr("其他"); } };

export function timezoneGroups(zones, current, date = new Date()) {
  const instant = new Date(date);
  const validDate = Number.isFinite(instant.getTime()) ? instant : new Date();
  const groups = new Map();
  for (const zone of new Set([...(Array.isArray(zones) ? zones : []), current].filter(z => typeof z === 'string' && z.trim()))) {
    let offset = '', name = '';
    try {
      const parts = new Intl.DateTimeFormat('en-US', { timeZone:zone, timeZoneName:'longOffset' }).formatToParts(validDate);
      offset = parts.find(p => p.type === 'timeZoneName').value.replace('GMT', 'UTC');
      name = new Intl.DateTimeFormat(language.value, { timeZone:zone, timeZoneName:'longGeneric' }).formatToParts(validDate).find(p => p.type === 'timeZoneName').value;
    } catch { /* Server tzdata can be newer than the browser; keep its valid choices. */ }
    const group = zone === 'UTC' ? tr("协调世界时") : (regions[zone.split('/')[0]] || tr("其他"));
    if (!groups.has(group)) groups.set(group, []);
    groups.get(group).push({ value:zone, label:[offset, name, zone].filter(Boolean).join(' · ') });
  }
  for (const options of groups.values()) options.sort((a,b) => a.value.localeCompare(b.value, 'en'));
  return [...groups].sort(([a],[b]) => a === tr("协调世界时") ? -1 : b === tr("协调世界时") ? 1 : a.localeCompare(b, language.value)).map(([label, options]) => ({label, options}));
}

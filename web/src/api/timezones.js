const regions = { Africa:'非洲', America:'美洲', Antarctica:'南极洲', Arctic:'北极地区', Asia:'亚洲', Atlantic:'大西洋', Australia:'澳大利亚', Europe:'欧洲', Indian:'印度洋', Pacific:'太平洋', Etc:'其他' };

export function timezoneGroups(zones, current, date = new Date()) {
  const instant = new Date(date);
  const validDate = Number.isFinite(instant.getTime()) ? instant : new Date();
  const groups = new Map();
  for (const zone of new Set([...(Array.isArray(zones) ? zones : []), current].filter(z => typeof z === 'string' && z.trim()))) {
    let offset = '', name = '';
    try {
      const parts = new Intl.DateTimeFormat('en-US', { timeZone:zone, timeZoneName:'longOffset' }).formatToParts(validDate);
      offset = parts.find(p => p.type === 'timeZoneName').value.replace('GMT', 'UTC');
      name = new Intl.DateTimeFormat('zh-CN', { timeZone:zone, timeZoneName:'longGeneric' }).formatToParts(validDate).find(p => p.type === 'timeZoneName').value;
    } catch { /* Server tzdata can be newer than the browser; keep its valid choices. */ }
    const group = zone === 'UTC' ? '协调世界时' : (regions[zone.split('/')[0]] || '其他');
    if (!groups.has(group)) groups.set(group, []);
    groups.get(group).push({ value:zone, label:[offset, name, zone].filter(Boolean).join(' · ') });
  }
  for (const options of groups.values()) options.sort((a,b) => a.value.localeCompare(b.value, 'en'));
  return [...groups].sort(([a],[b]) => a === '协调世界时' ? -1 : b === '协调世界时' ? 1 : a.localeCompare(b, 'zh-CN')).map(([label, options]) => ({label, options}));
}

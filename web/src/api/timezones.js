import { language } from '../i18n/index.js';

export function timezoneGroups(zones, current, date = new Date()) {
  const instant = new Date(date);
  const reference = Number.isFinite(instant.getTime()) ? instant : new Date();
  const options = new Map();
  const values = [...new Set(['UTC', ...(Array.isArray(zones) ? zones : []), current])]
    .filter(zone => typeof zone === 'string' && /^(?:UTC|[A-Za-z][\w+-]*(?:\/[\w+-]+)*)$/.test(zone))
    .sort((a, b) => a.localeCompare(b, 'en'));
  for (const zone of values) {
    try {
      const formatter = new Intl.DateTimeFormat('en-US', { timeZone: zone, timeZoneName: 'longOffset' });
      const canonical = formatter.resolvedOptions().timeZone;
      const offset = formatter.formatToParts(reference).find(part => part.type === 'timeZoneName')?.value;
      const match = /^GMT([+-])(\d{2}):(\d{2})$/.exec(offset);
      if (offset !== 'GMT' && !match) continue;
      const minutes = match ? (Number(match[2]) * 60 + Number(match[3])) * (match[1] === '-' ? -1 : 1) : 0;
      const existing = options.get(canonical);
      if (existing && zone !== current && (existing.value === current || zone !== canonical)) continue;
      const name = new Intl.DateTimeFormat(language.value, { timeZone: zone, timeZoneName: 'longGeneric' })
        .formatToParts(reference).find(part => part.type === 'timeZoneName')?.value;
      options.set(canonical, { value: zone, label: name && name !== zone ? name + ' · ' + zone : zone, minutes });
    } catch {
      // Unsupported or malformed identifiers cannot provide a reliable offset.
    }
  }
  const groups = new Map();
  for (const { value, label, minutes } of [...options.values()].sort((a, b) => a.minutes - b.minutes || a.value.localeCompare(b.value, 'en'))) {
    const hours = String(Math.floor(Math.abs(minutes) / 60)).padStart(2, '0');
    const remainder = String(Math.abs(minutes) % 60).padStart(2, '0');
    const offset = 'UTC' + (minutes < 0 ? '-' : '+') + hours + ':' + remainder;
    if (!groups.has(offset)) groups.set(offset, []);
    groups.get(offset).push({ value, label: offset + ' · ' + label });
  }
  return [...groups].map(([label, options]) => ({ label, options }));
}

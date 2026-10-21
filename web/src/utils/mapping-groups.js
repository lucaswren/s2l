import { tr } from '../i18n/index.js';
// Encoded group values stay stable across languages; '*' is reserved for all.
export const mappingGroupKey = mapping => JSON.stringify(mapping.group || '');
export function mappingGroups(mappings) {
  return [...new Set(mappings.map(row => row.group || ''))].sort().map(group => ({value:JSON.stringify(group), label:group || tr('未分组')}));
}

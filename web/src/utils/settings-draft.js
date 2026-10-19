// Refresh server state without discarding independent, unsaved form edits.
// Settings drafts contain primitive values; compare against the last server snapshot.
export function mergeSettingsDraft(current, previous, next) {
 const merged = {...current};
 for (const [key,value] of Object.entries(next)) {
  if (!previous || Object.is(current[key], previous[key])) merged[key] = value;
 }
 return merged;
}

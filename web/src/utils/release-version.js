const stableVersion = /^v?([0-9]+)\.([0-9]+)\.([0-9]+)$/;

export function isOlderStableRelease(candidate, current) {
  const a = stableVersion.exec(candidate || '');
  const b = stableVersion.exec(current || '');
  if (!a || !b) return false;
  for (let i = 1; i <= 3; i++) {
    const left = BigInt(a[i]);
    const right = BigInt(b[i]);
    if (left !== right) return left < right;
  }
  return false;
}

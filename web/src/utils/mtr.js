export function mtrHops(output = '') {
 const hops = new Map();
 const lines = output.split('\n');
 lines.pop(); // Streaming chunks may end in the middle of a raw event.
 for (const line of lines) {
  const [type, position, value, sequence] = line.trim().split(/\s+/);
  const index = Number(position);
  if (!['h','x','p'].includes(type) || !/^\d+$/.test(position || '') || index < 0 || index >= 20) continue;
  if (!hops.has(index)) hops.set(index, {hop:index+1, addresses:new Set(), sent:new Set(), replies:new Map()});
  const hop = hops.get(index);
  if (type === 'h' && /^[0-9a-fA-F:.%]+$/.test(value || '')) hop.addresses.add(value);
  if (type === 'x' && /^\d+$/.test(value || '')) hop.sent.add(value);
  if (type === 'p' && /^\d+$/.test(value || '') && /^\d+$/.test(sequence || '')) hop.replies.set(sequence, Number(value) / 1000);
 }
 const last = Math.max(-1, ...hops.keys());
 return Array.from({length:last+1}, (_,index) => {
  const hop = hops.get(index), times = hop ? [...hop.replies.values()] : [];
  return {hop:index+1, address:hop?.addresses.size ? [...hop.addresses].join(' / ') : '—', sent:hop?.sent.size || 0, received:times.length, last:times.length ? times.at(-1).toFixed(2) : '—', average:times.length ? (times.reduce((a,b) => a+b,0)/times.length).toFixed(2) : '—'};
 });
}

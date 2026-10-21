export function managementAddresses(info, panelHost = '') {
  const valid = value => typeof value === 'string' && /^(?:\d{1,3}\.){3}\d{1,3}$/.test(value) && value.split('.').every(part => String(Number(part)) === part && Number(part) <= 255);
  const addresses = [...new Set((info.local_ipv4 || []).filter(valid))];
  const existing = info.access?.destination_ip || '';
  // The panel host is a suggestion only when the server confirms it is local.
  const suggested = addresses.includes(panelHost) ? panelHost : addresses.length === 1 ? addresses[0] : '';
  return { addresses, selected: existing || suggested, automatic: !existing && !!suggested };
}

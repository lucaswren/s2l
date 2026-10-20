export function clearCredentials() {
  authorization = "";
}
let authorization = "";
export function setCredentials(user, password) {
  const bytes = new TextEncoder().encode(user + ":" + password);
  authorization = "Basic " + btoa(String.fromCharCode(...bytes));
}
async function request(path, options = {}) {
  const res = await fetch(path, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...(authorization ? { Authorization: authorization } : {}),
      ...(options.headers || {}),
    },
  });
  let data;
  try { data = await res.json(); }
  catch (e) {
    if (e.name === "AbortError") throw e;
    if (res.ok) throw new Error("服务器返回了无效数据，请刷新后重试");
  }
  if (!res.ok) {
    throw new Error(data?.error || res.statusText || `HTTP ${res.status}`);
  }
  if (data === null || typeof data !== "object") {
    throw new Error("服务器返回了无效数据，请刷新后重试");
  }
  const revision=res.headers?.get("ETag");if(revision && !Array.isArray(data))Object.defineProperty(data,"_revision",{value:revision});
  return data;
}

async function streamNetworkTest(id, body, signal, onEvent) {
 const res = await fetch(`/api/nodes/${id}/network-test`, {
  method: 'POST', signal, body: JSON.stringify({...body, stream:true}),
  headers: {'Content-Type':'application/json', ...(authorization ? {Authorization:authorization} : {})},
 });
 if (!res.ok) {
  const data = await res.json().catch(e => { if (e.name === 'AbortError') throw e; return {}; });
  throw new Error(data.error || res.statusText || `HTTP ${res.status}`);
 }
 if (res.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase() !== 'application/x-ndjson' || !res.body) throw new Error('服务器不支持实时追踪，请更新服务后重试');
 const reader = res.body.getReader(), decoder = new TextDecoder();
 let pending = '', total = 0, finalResult = null, started = false;
 const consume = line => {
  if (!line.trim()) return;
  let event;
  try { event = JSON.parse(line); } catch { throw new Error('追踪数据格式无效'); }
  if (!event || !['start','output','result'].includes(event.type) || (event.type === 'output' ? typeof event.data !== 'string' : !event.data || typeof event.data !== 'object' || Array.isArray(event.data))) throw new Error('追踪数据格式无效');
  if (finalResult || (event.type !== 'start' && !started) || (event.type === 'start' && started)) throw new Error('追踪数据顺序无效');
  if (event.type === 'start') {
   if (event.data.kind !== 'mtr') throw new Error('追踪数据格式无效');
   started = true;
  }
  if (event.type === 'result' && (event.data.kind !== 'mtr' || typeof event.data.ok !== 'boolean' || typeof event.data.output !== 'string')) throw new Error('追踪结束结果无效');
  onEvent?.(event);
  if (event.type === 'result') finalResult = event.data;
 };
 try {
  while (true) {
   const {value, done} = await reader.read();
   if (done) break;
   total += value.byteLength;
   if (total > 512 * 1024) throw new Error('追踪输出超过限制');
   pending += decoder.decode(value, {stream:true});
   let end;
   while ((end = pending.indexOf('\n')) !== -1) { consume(pending.slice(0,end)); pending = pending.slice(end+1); }
  }
  pending += decoder.decode();
  if (pending.trim()) consume(pending);
  if (!finalResult) throw new Error('追踪连接中断，已保留收到的结果');
  return finalResult;
 } finally {
  await reader.cancel().catch(() => {});
  reader.releaseLock();
 }
}

export const api = {
 operations:(signal)=>request('/api/operations',{signal}),
 saveNotifications:(body)=>request('/api/operations',{method:'POST',body:JSON.stringify(body)}),
 disconnectSession:(row)=>request('/api/sessions/disconnect',{method:'POST',body:JSON.stringify({interface:row.interface,peer:row.peer_ip,index:row.index})}),
 connections:(id,signal)=>request(`/api/mappings/${encodeURIComponent(id)}/connections`,{signal}),
 closeConnection:(id,connection)=>request(`/api/mappings/${encodeURIComponent(id)}/connections`,{method:'POST',body:JSON.stringify({id:connection})}),
 previewNode:(body,revision)=>request('/api/nodes?preview=1',{method:'POST',headers:revision?{'If-Match':revision}:{},body:JSON.stringify(body)}),
 previewMapping:(body,revision)=>request('/api/mappings?preview=1',{method:'POST',headers:revision?{'If-Match':revision}:{},body:JSON.stringify(body)}),
  history:(id,signal)=>request(`/api/history?mapping_id=${encodeURIComponent(id)}`,{signal}),
  mappingDiagnostic:(id,signal)=>request(`/api/mappings/${encodeURIComponent(id)}/network-test`,{method:"POST",body:"{}",signal}),
  updateStatus: (signal) => request("/api/settings/update", {signal}),
  updateSoftware: (body) => request("/api/settings/update", {method:"POST", body:JSON.stringify(body)}),
  timeSettings: () => request("/api/settings/time"),
  saveTimeSettings: (body) => request("/api/settings/time", {method:"POST", body:JSON.stringify(body)}),
  backups: () => request("/api/backups"),
  createBackup: () => request("/api/backups", {method:"POST",body:"{}"}),
  previewBackup: (id) => request("/api/backups/preview", {method:"POST",body:JSON.stringify({id})}),
  restoreBackup: (body) => request("/api/backups/restore", {method:"POST",body:JSON.stringify(body)}),
  debugAnalysis: () => request("/api/debug/analysis"),
  debug: (signal) => request("/api/debug", {signal}),
  setDebug: (enabled) => request("/api/debug", {method:"POST", body:JSON.stringify({enabled})}),
  exportDebug: async () => {
    const res = await fetch("/api/debug/export", {headers: authorization ? {Authorization: authorization} : {}});
    if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || "Export failed");
    if (res.headers.get("Content-Type")?.split(";")[0].trim().toLowerCase() !== "application/zip") {
      throw new Error("导出响应不是日志 ZIP，请稍后重试");
    }
    const blob = await res.blob();
    const signature = new Uint8Array(await blob.slice(0, 4).arrayBuffer());
    if (signature.length !== 4 || signature[0] !== 0x50 || signature[1] !== 0x4b || signature[2] !== 3 || signature[3] !== 4) {
      throw new Error("日志 ZIP 内容无效，请重新导出");
    }
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a"); a.href = url; a.download = "s2l-debug.zip"; a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  },
  streamNetworkTest,
  networkTestNode: (id, body, signal) => request(`/api/nodes/${id}/network-test`, {method:"POST", body:JSON.stringify(body), signal}),
  listNodes: () => request("/api/nodes"),
  getNode: (id) => request(`/api/nodes/${id}`),
  saveNode: (body,revision) =>
    request("/api/nodes", { method: "POST",
      headers: revision ? {"If-Match":revision} : {}, body: JSON.stringify(body) }),
  deleteNode: (id) => request(`/api/nodes/${id}`, { method: "DELETE" }),
  importNodes: (body) =>
    request("/api/nodes/import", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  speedTestNode: (id, bytes) =>
    request(`/api/nodes/${id}/speedtest`, {
      method: "POST",
      body: JSON.stringify(bytes ? { bytes } : {}),
    }),

  listMappings: () => request("/api/mappings"),
  getMapping: (id) => request(`/api/mappings/${id}`),
  saveMapping: (body,revision) =>
    request("/api/mappings", { method: "POST",
      headers: revision ? {"If-Match":revision} : {}, body: JSON.stringify(body) }),
  deleteMapping: (id) => request(`/api/mappings/${id}`, { method: "DELETE" }),
  importMappings: (body) =>
    request("/api/mappings/import", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  mappingAction: (id, action) =>
    request(`/api/mappings/${id}/action`, {
      method: "POST",
      body: JSON.stringify({ action }),
    }),

  saveIPsec: (body) =>
    request("/api/settings/ipsec", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  settings: () => request("/api/settings"),
  saveAccount: (body) =>
    request("/api/settings/account", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  saveWebPort: (body) =>
    request("/api/settings/web-port", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  saveSSH: (body) =>
    request("/api/settings/ssh", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  saveSSHPassword: (body) =>
    request("/api/settings/ssh-password", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  status: () => request("/api/status"),
};

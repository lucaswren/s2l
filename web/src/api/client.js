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
  return data;
}

export const api = {
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
  networkTestNode: (id, body, signal) => request(`/api/nodes/${id}/network-test`, {method:"POST", body:JSON.stringify(body), signal}),
  listNodes: () => request("/api/nodes"),
  getNode: (id) => request(`/api/nodes/${id}`),
  saveNode: (body) =>
    request("/api/nodes", { method: "POST", body: JSON.stringify(body) }),
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
  saveMapping: (body) =>
    request("/api/mappings", { method: "POST", body: JSON.stringify(body) }),
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

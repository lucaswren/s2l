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
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(data.error || res.statusText || "request failed");
  }
  return data;
}

export const api = {
  backups: () => request("/api/backups"),
  createBackup: () => request("/api/backups", {method:"POST",body:"{}"}),
  previewBackup: (id) => request("/api/backups/preview", {method:"POST",body:JSON.stringify({id})}),
  restoreBackup: (body) => request("/api/backups/restore", {method:"POST",body:JSON.stringify(body)}),
  debugAnalysis: () => request("/api/debug/analysis"),
  debug: () => request("/api/debug"),
  setDebug: (enabled) => request("/api/debug", {method:"POST", body:JSON.stringify({enabled})}),
  exportDebug: async () => {
    const res = await fetch("/api/debug/export", {headers: authorization ? {Authorization: authorization} : {}});
    if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || "Export failed");
    const url = URL.createObjectURL(await res.blob());
    const a = document.createElement("a"); a.href = url; a.download = "s2l-debug.zip"; a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  },
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

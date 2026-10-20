export const healthLabels = {checking:'检测中',healthy:'正常',unstable:'连接不稳定',unavailable:'不可用',recovering:'恢复中','circuit-open':'恢复熔断',degraded:'部分可用',unsupported:'不支持',restricted:'目标已响应 · 状态异常'};
export const stageLabels = {'proxy-connect':'节点连接','socks-auth':'SOCKS5 认证','target-connect':'目标连接',tls:'TLS 握手',http:'HTTP 响应',forwarding:'映射转发','test-entry':'测试入口',cancelled:'已取消或超时'};
export function protocolSummary(result) {
 return [stageLabels[result?.stage], result?.status_code ? `HTTP ${result.status_code}` : '', Number.isFinite(result?.duration_ms) ? `${result.duration_ms} ms` : ''].filter(Boolean).join(' · ');
}
export function passRate(counts) {
 const total=(counts?.pass||0)+(counts?.fail||0)+(counts?.restricted||0)+(counts?.unsupported||0);
 return total ? `${((counts?.pass||0)/total*100).toFixed(1)}%` : '—';
}

export function isResolved(incident){const value=incident?.resolved;return typeof value==='string'&&!value.startsWith('0001-')&&Number.isFinite(Date.parse(value));}

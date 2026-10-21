import { tr } from '../i18n/index.js';
export const healthLabels = {get checking() { return tr("检测中"); },get healthy() { return tr("正常"); },get unstable() { return tr("连接不稳定"); },get unavailable() { return tr("不可用"); },get recovering() { return tr("恢复中"); },get 'circuit-open'() { return tr("恢复熔断"); },get degraded() { return tr("部分可用"); },get unsupported() { return tr("不支持"); },get restricted() { return tr("目标已响应 · 状态异常"); }};
export const stageLabels = {get 'proxy-connect'() { return tr("节点连接"); },get 'socks-auth'() { return tr("SOCKS5 认证"); },get 'target-connect'() { return tr("目标连接"); },get tls() { return tr("TLS 握手"); },get http() { return tr("HTTP 响应"); },get forwarding() { return tr("映射转发"); },get 'test-entry'() { return tr("测试入口"); },get cancelled() { return tr("已取消或超时"); }};
export function protocolSummary(result) {
 return [stageLabels[result?.stage], result?.status_code ? `HTTP ${result.status_code}` : '', Number.isFinite(result?.duration_ms) ? `${result.duration_ms} ms` : ''].filter(Boolean).join(' · ');
}
export function passRate(counts) {
 const total=(counts?.pass||0)+(counts?.fail||0)+(counts?.restricted||0)+(counts?.unsupported||0);
 return total ? `${((counts?.pass||0)/total*100).toFixed(1)}%` : '—';
}

export function isResolved(incident){const value=incident?.resolved;return typeof value==='string'&&!value.startsWith('0001-')&&Number.isFinite(Date.parse(value));}

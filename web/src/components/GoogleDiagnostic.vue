<script setup>
import { tr, dateTime, clockTime } from '../i18n/index.js';

import {computed,ref} from 'vue';
import {compareGoogle,readGoogleSnapshot,rejectEvidence} from '../utils/google-diagnostic';
const emit=defineEmits(['baseline']);
const baseline=ref(null),importError=ref(''),showAll=ref(false);
const reachable=t=>t.tcp==='healthy'&&t.tls==='healthy'&&t.http==='responded';
const state=t=>t.a?.state!=='healthy'?tr("DNS 异常"):t.tcp!=='healthy'?tr("TCP 异常"):t.tls!=='healthy'?tr("TLS 异常"):t.http!=='responded'?tr("HTTP 未完成"):tr("可达");
async function importSnapshot(event){try{const file=event.target.files?.[0];if(!file)return;if(file.size>512*1024)throw Error(tr("对照文件超过 512 KiB"));baseline.value=readGoogleSnapshot(await file.text());emit('baseline',baseline.value);importError.value='';}catch(e){importError.value=e instanceof SyntaxError?tr("文件不是有效的 JSON"):e.message;}finally{event.target.value='';}}
const props=defineProps({result:Object});
const labels={get healthy() { return tr("正常"); },get unavailable() { return tr("失败"); },get not_tested() { return tr("未检测"); },get cancelled() { return tr("已取消"); },get no_address() { return tr("无地址"); },get nxdomain() { return tr("域名不存在"); },get responded() { return tr("已响应"); }};
const text=v=>labels[v]||v||tr("未检测");
const comparisons=computed(()=>compareGoogle(props.result.google?.[0],baseline.value||props.result.google?.[1]));
const differences=computed(()=>comparisons.value.filter(r=>r.different||r.dnsDifferent));
const visibleComparisons=computed(()=>showAll.value?comparisons.value:differences.value);
</script>
<template>
 <section class="google-diagnostic">
  <details class="google-tools"><summary>{{ tr("历史结果对照") }}</summary><label class="snapshot-import">{{ tr("导入历史线路结果进行对照") }}<input type="file" accept="application/json,.json" @change="importSnapshot" /></label><p v-if="importError" role="alert">{{importError}}</p>
  </details>
  <p v-if="baseline">{{ tr("历史对照：") }}{{baseline.node_name}} · {{dateTime(baseline.checked_at)}}{{ tr("（非实时）") }}<el-button text @click="baseline=null;emit('baseline',null)">{{ tr("移除") }}</el-button></p>
  <p v-else-if="result.google?.[1]">{{ tr("当前节点") }} {{result.google[0].node_name}} {{ tr("对照") }} {{result.google[1].node_name}}</p>

  <div v-if="comparisons.length" class="google-comparison"><h4>{{ tr("线路对照 ·") }} {{differences.length}} {{ tr("项差异") }}</h4><el-button text @click="showAll=!showAll">{{showAll?tr("仅看差异"):tr("查看全部目标")}}</el-button><p v-if="!visibleComparisons.length">{{ tr("检测状态一致，未发现分项差异。") }}</p><article v-for="row in visibleComparisons" :key="row.host"><strong>{{row.host}}</strong><span>{{row.different?tr("阶段差异：")+row.fields.join(' / '):tr("检测状态一致，不代表 App 行为一致")}}{{row.dnsDifferent?tr(" · DNS 地址不同（不等于故障）"):''}}</span><p>{{ tr("当前：DoH") }} {{text(row.a.doh?.state)}} / TCP DNS {{text(row.a.tcp_dns?.state)}} · TCP {{text(row.a.tcp)}} · TLS {{text(row.a.tls)}} · HTTP {{row.a.status || text(row.a.http)}}</p><p v-if="row.b">{{ tr("对照：DoH") }} {{text(row.b.doh?.state)}} / TCP DNS {{text(row.b.tcp_dns?.state)}} · TCP {{text(row.b.tcp)}} · TLS {{text(row.b.tls)}} · HTTP {{row.b.status || text(row.b.http)}}</p></article></div>
  <section v-for="report in result.google" :key="report.node_id" class="google-line">
   <h4>{{report.node_name}} {{ tr("· 出口") }} {{report.exit?.exit_ip || tr("未确认")}}</h4>
   <p>{{report.targets.filter(reachable).length}} / {{report.targets.length}} {{ tr("目标可达 ·") }} {{clockTime(report.checked_at)}}</p>
   <details v-for="row in report.targets" :key="row.host" class="google-target"><summary><strong>{{row.host}}</strong><span>{{state(row)}}{{row.status?' · HTTP '+row.status:''}}</span></summary>
    <p>DoH A：{{text(row.doh?.state)}} {{row.doh?.addresses?.join(', ') || '—'}} · {{row.doh?.duration_ms}} ms / TCP DNS A：{{text(row.tcp_dns?.state)}} {{row.tcp_dns?.addresses?.join(', ') || '—'}} · {{row.tcp_dns?.duration_ms}} ms</p>
    <p>A：{{text(row.a.state)}} · {{row.a.addresses?.join(', ') || '—'}} · {{row.a.duration_ms}} ms</p>
    <p>AAAA：{{text(row.aaaa.state)}} · {{row.aaaa.addresses?.join(', ') || '—'}} · {{row.aaaa.duration_ms}} ms</p>
    <p>TCP {{text(row.tcp)}} {{row.tcp_ms ?? '—'}} ms · TLS {{text(row.tls)}} {{row.tls_ms ?? '—'}} ms · HTTP {{row.status || text(row.http)}} · {{row.alpn || tr("未协商 ALPN")}}</p>
    <p v-if="row.ip">IP {{row.ip}}</p>
    <p v-if="row.error" class="error-text">{{row.error_code}} · {{row.error}}</p>
    <p v-if="row.attempts?.length>1 && row.http==='responded'" class="muted">{{ tr('复测后可达，部分地址失败') }}</p>
    <details v-if="row.attempts?.length>1"><summary>{{ tr('逐地址结果') }}</summary><p v-for="attempt in row.attempts" :key="attempt.ip">{{attempt.ip}} · TCP {{text(attempt.tcp)}} · TLS {{text(attempt.tls)}} · HTTP {{attempt.status || text(attempt.http)}}<br v-if="attempt.error" /><span v-if="attempt.error" class="error-text">{{attempt.error_code}} · {{attempt.error}}</span></p></details>
    <p>{{ tr("阶段") }} {{row.stage}}{{row.timed_out?tr(" · 超时"):row.cancelled?tr(" · 已取消"):''}} · TCP/TLS/HTTP {{row.duration_ms}} ms</p>
   </details>
   <p><strong>{{ tr("QUIC 回退：待客户端验证") }}</strong> {{ tr("· TCP/TLS 替代路径") }} {{report.tcp_alternatives ?? report.targets.filter(t=>t.tcp==='healthy'&&t.tls==='healthy').length}} / {{report.targets.length}}</p>
  </section>
  <details><summary>{{ tr("映射策略与共享连接证据") }}</summary><p v-if="!result.mapping_policies?.length">{{ tr("所选节点暂无映射。") }}</p><p v-for="m in result.mapping_policies" :key="m.mapping_id">{{m.mapping_id}} · {{m.node_id}} · UDP {{m.udp_policy}} · DNS {{m.dns_mode}} · {{m.status}}<br />{{ tr("TCP 连接上限") }} {{m.max_connections || tr("不限")}}{{ tr("（映射网段合计） · PPP 会话上限") }} {{m.max_sessions || tr("不限")}} {{ tr("· 上/下行") }} {{m.upload_kbps || 0}} / {{m.download_kbps || 0}} {{ tr("Kbps（0 为不限）") }}<br />{{rejectEvidence(m)}}</p></details>
  <details class="google-scope"><summary>{{ tr("检测范围与说明") }}</summary><p>{{ tr("HTTP 状态码仅作诊断参考，不代表账号或 App 可用。") }}</p><p>{{tr(result.google?.[0]?.scope || '')}}</p><p>{{tr(result.google?.[0]?.quic || '')}}</p>
  <p>{{ tr("计数为映射合计，仅表示规则命中；客户端是否收到 ICMP 需另行验证。") }}</p>
  <p>{{ tr("多设备可共享出口；出现相互影响时，检查连接限额与会话状态。节点检测不经过映射限额。") }}</p>
  <p>{{ tr("AAAA 记录不代表 IPv6 可用。本次检查 IPv4；客户端回退需实际验证。") }}</p></details>
 </section>
</template>
<style scoped>
.google-diagnostic{font-size:14px;line-height:1.65;overflow-wrap:anywhere}.google-line{min-width:0;max-width:100%;box-sizing:border-box;margin:16px 0;padding:12px;border:1px solid var(--el-border-color-light);border-radius:8px}.google-target{padding:10px 0;border-bottom:1px solid var(--el-border-color-lighter)}p{margin:8px 0;line-height:1.65}details{min-width:0}summary{min-width:0;cursor:pointer;min-height:36px;line-height:1.7}.google-target summary{display:flex;justify-content:space-between;gap:8px;flex-wrap:wrap}.google-target summary strong{min-width:0;max-width:100%}.google-target summary span{color:var(--el-text-color-secondary)}.google-tools,.google-scope{margin:10px 0}.snapshot-import{display:grid;gap:8px}.snapshot-import input{width:100%;min-width:0;max-width:100%;box-sizing:border-box;min-height:32px;font-size:14px}.google-comparison article{display:flex;flex-wrap:wrap;justify-content:space-between;gap:6px;padding:8px 0}.google-comparison article p{flex-basis:100%}
</style>

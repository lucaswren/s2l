<script setup>
import {computed,ref} from 'vue';
import {compareGoogle,readGoogleSnapshot,rejectEvidence} from '../utils/google-diagnostic';
const emit=defineEmits(['baseline']);
const baseline=ref(null),importError=ref(''),showAll=ref(false);
const reachable=t=>t.tcp==='healthy'&&t.tls==='healthy'&&t.http==='responded';
const state=t=>t.a?.state!=='healthy'?'DNS 异常':t.tcp!=='healthy'?'TCP 异常':t.tls!=='healthy'?'TLS 异常':t.http!=='responded'?'HTTP 未完成':'可达';
async function importSnapshot(event){try{const file=event.target.files?.[0];if(!file)return;if(file.size>512*1024)throw Error('对照文件超过 512 KiB');baseline.value=readGoogleSnapshot(await file.text());emit('baseline',baseline.value);importError.value='';}catch(e){importError.value=e instanceof SyntaxError?'文件不是有效的 JSON':e.message;}finally{event.target.value='';}}
const props=defineProps({result:Object});
const labels={healthy:'正常',unavailable:'失败',not_tested:'未检测',cancelled:'已取消',no_address:'无地址',nxdomain:'域名不存在',responded:'已响应'};
const text=v=>labels[v]||v||'未检测';
const comparisons=computed(()=>compareGoogle(props.result.google?.[0],baseline.value||props.result.google?.[1]));
const differences=computed(()=>comparisons.value.filter(r=>r.different||r.dnsDifferent));
const visibleComparisons=computed(()=>showAll.value?comparisons.value:differences.value);
</script>
<template>
 <section class="google-diagnostic">
  <details class="google-tools"><summary>历史结果对照</summary><label class="snapshot-import">导入历史线路结果进行对照<input type="file" accept="application/json,.json" @change="importSnapshot" /></label><p v-if="importError" role="alert">{{importError}}</p>
  </details>
  <p v-if="baseline">历史对照：{{baseline.node_name}} · {{new Date(baseline.checked_at).toLocaleString()}}（非实时）<el-button text @click="baseline=null;emit('baseline',null)">移除</el-button></p>
  <p v-else-if="result.google?.[1]">当前节点 {{result.google[0].node_name}} 对照 {{result.google[1].node_name}}</p>

  <div v-if="comparisons.length" class="google-comparison"><h4>线路对照 · {{differences.length}} 项差异</h4><el-button text @click="showAll=!showAll">{{showAll?'仅看差异':'查看全部目标'}}</el-button><p v-if="!visibleComparisons.length">检测状态一致，未发现分项差异。</p><article v-for="row in visibleComparisons" :key="row.host"><strong>{{row.host}}</strong><span>{{row.different?'阶段差异：'+row.fields.join(' / '):'检测状态一致，不代表 App 行为一致'}}{{row.dnsDifferent?' · DNS 地址不同（不等于故障）':''}}</span><p>当前：DoH {{text(row.a.doh?.state)}} / TCP DNS {{text(row.a.tcp_dns?.state)}} · TCP {{text(row.a.tcp)}} · TLS {{text(row.a.tls)}} · HTTP {{row.a.status || text(row.a.http)}}</p><p v-if="row.b">对照：DoH {{text(row.b.doh?.state)}} / TCP DNS {{text(row.b.tcp_dns?.state)}} · TCP {{text(row.b.tcp)}} · TLS {{text(row.b.tls)}} · HTTP {{row.b.status || text(row.b.http)}}</p></article></div>
  <section v-for="report in result.google" :key="report.node_id" class="google-line">
   <h4>{{report.node_name}} · 出口 {{report.exit?.exit_ip || '未确认'}}</h4>
   <p>{{report.targets.filter(reachable).length}} / {{report.targets.length}} 目标可达 · {{new Date(report.checked_at).toLocaleTimeString()}}</p>
   <details v-for="row in report.targets" :key="row.host" class="google-target"><summary><strong>{{row.host}}</strong><span>{{state(row)}}{{row.status?' · HTTP '+row.status:''}}</span></summary>
    <p>DoH A：{{text(row.doh?.state)}} {{row.doh?.addresses?.join(', ') || '—'}} · {{row.doh?.duration_ms}} ms / TCP DNS A：{{text(row.tcp_dns?.state)}} {{row.tcp_dns?.addresses?.join(', ') || '—'}} · {{row.tcp_dns?.duration_ms}} ms</p>
    <p>A：{{text(row.a.state)}} · {{row.a.addresses?.join(', ') || '—'}} · {{row.a.duration_ms}} ms</p>
    <p>AAAA：{{text(row.aaaa.state)}} · {{row.aaaa.addresses?.join(', ') || '—'}} · {{row.aaaa.duration_ms}} ms</p>
    <p>TCP {{text(row.tcp)}} {{row.tcp_ms ?? '—'}} ms · TLS {{text(row.tls)}} {{row.tls_ms ?? '—'}} ms · HTTP {{row.status || text(row.http)}} · {{row.alpn || '未协商 ALPN'}}</p>
    <p>阶段 {{row.stage}}{{row.timed_out?' · 超时':row.cancelled?' · 已取消':''}} · TCP/TLS/HTTP {{row.duration_ms}} ms</p>
   </details>
   <p><strong>QUIC 回退：待客户端验证</strong> · TCP/TLS 替代路径 {{report.tcp_alternatives ?? report.targets.filter(t=>t.tcp==='healthy'&&t.tls==='healthy').length}} / {{report.targets.length}}</p>
  </section>
  <details><summary>映射策略与共享连接证据</summary><p v-if="!result.mapping_policies?.length">所选节点暂无映射。</p><p v-for="m in result.mapping_policies" :key="m.mapping_id">{{m.mapping_id}} · {{m.node_id}} · UDP {{m.udp_policy}} · DNS {{m.dns_mode}} · {{m.status}}<br />TCP 连接上限 {{m.max_connections || '不限'}}（映射网段合计） · PPP 会话上限 {{m.max_sessions || '不限'}} · 上/下行 {{m.upload_kbps || 0}} / {{m.download_kbps || 0}} Kbps（0 为不限）<br />{{rejectEvidence(m)}}</p></details>
  <details class="google-scope"><summary>检测范围与说明</summary><p>HTTP 401/403/404 代表收到响应，不等于出口被封。代表性目标不覆盖 App 全部接口；不发送 UDP/QUIC，不证明手机已经回退 TCP。</p><p>{{result.google?.[0]?.scope}}</p><p>{{result.google?.[0]?.quic}}</p>
  <p>拒绝计数按映射合计，增加只证明服务器规则命中，不证明 ICMP 到达手机，也不能归属到某个设备。若要采样，检测期间在手机重现问题。</p>
  <p>多设备默认允许共享出口。若电脑在线时手机失败，优先对比映射总连接额度、上游连接额度和 Panabit 会话状态；节点专项检测绕过映射限额，不能排除该类问题。</p>
  <p>AAAA 有地址不代表 IPv6 路径可用。本次业务连接仅检测 IPv4 和每个目标的首个 A 地址；Panabit 分流、DNS 缓存与手机侧 ICMP 返回需结合客户端复现核对。</p></details>
 </section>
</template>
<style scoped>
.google-diagnostic{font-size:13px;line-height:1.7;overflow-wrap:anywhere}.google-line{min-width:0;max-width:100%;box-sizing:border-box;margin:16px 0;padding:12px;border:1px solid var(--el-border-color-light);border-radius:8px}.google-target{padding:10px 0;border-bottom:1px solid var(--el-border-color-lighter)}p{margin:6px 0}details{min-width:0}summary{min-width:0;cursor:pointer;min-height:36px;line-height:1.7}.google-target summary{display:flex;justify-content:space-between;gap:8px;flex-wrap:wrap}.google-target summary strong{min-width:0;max-width:100%}.google-target summary span{color:var(--el-text-color-secondary)}.google-tools,.google-scope{margin:10px 0}.snapshot-import{display:grid;gap:8px}.snapshot-import input{width:100%;min-width:0;max-width:100%;box-sizing:border-box;min-height:44px;font-size:16px}.google-comparison article{display:flex;flex-wrap:wrap;justify-content:space-between;gap:6px;padding:8px 0}.google-comparison article p{flex-basis:100%}
</style>

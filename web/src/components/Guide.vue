<script setup>
import { tr } from '../i18n/index.js';

import {computed,onMounted,ref,watch} from 'vue';
import {api} from '../api/client';
import {clientAccessText} from '../utils/client-access.js';
const props=defineProps({mappings:{type:Array,default:()=>[]},health:{type:Object,default:()=>({})}});
const host = location.hostname;
const selected=ref('');
const settings=ref(null);
const error=ref('');
const copied=ref(false);
const mapping=computed(()=>props.mappings.find(m=>m.id===selected.value));
const local=computed(()=>props.health[selected.value]);
const clientDNS=computed(()=>settings.value?.client_dns?.join(', ') || tr("等待服务端 DNS 参数"));
watch(()=>props.mappings,list=>{if(!list.some(m=>m.id===selected.value)) selected.value=(list.find(m=>m.status==='running')||list[0])?.id||'';},{immediate:true});
const readiness=computed(()=>{
  if(!mapping.value)return tr("请先选择映射");
  if(mapping.value.status!=='running')return tr("映射未运行，请先启动");
  if(!local.value)return tr("等待服务端链路检查");
  if(local.value.local_state!=='healthy')return tr("本地链路异常或恢复中，请查看运行监控");
  if(local.value.tcp?.state!=='healthy')return tr("本地链路已启动，上游 TCP 尚未验证正常");
  return tr("本地链路和上游 TCP 检查正常；仍需客户端实际拨号验证");
});
onMounted(async()=>{try{settings.value=await api.settings();}catch(e){error.value=e.message;}});
async function copy(){
  if(!mapping.value)return;
  const text=clientAccessText(host,mapping.value,settings.value,tr);
  try{await navigator.clipboard.writeText(text);copied.value=true;error.value='';}catch{error.value=tr("浏览器不允许复制，请手动选中连接参数");}
}
</script>
<template>
  <div class="section-head">
    <div>
      <h2>{{ tr("快速接入") }}</h2>
      <p class="muted">{{ tr("导入节点 → 创建映射 → 启动 → 客户端拨号") }}</p>
    </div>
  </div>
  <el-card shadow="never" class="section-gap">
    <template #header>{{ tr("连接准备检查") }}</template>
    <el-select v-model="selected" :placeholder='tr("选择映射")' :aria-label='tr("接入映射")' @change="copied=false">
      <el-option v-for="m in mappings" :key="m.id" :value="m.id" :label="m.tun_name + ' · ' + m.l2tp_user" />
    </el-select>
    <p>{{ readiness }}</p>
    <p v-if="settings?.ipsec">IPsec：{{ settings.ipsec.enabled ? (settings.ipsec.active ? tr("已启用，服务活动") : tr("已启用，服务未活动")) : tr("未启用") }}</p>
    <p v-else>{{ tr("IPsec 状态尚未获取，请在系统设置中确认。") }}</p>
    <el-alert v-if="error" :title="error" type="warning" :closable="false" />
    <el-button :disabled="!mapping" @click="copy">{{ copied ? tr("连接参数已复制") : tr("复制连接参数（不含密码）") }}</el-button>
    <p class="muted">{{ tr("此检查不代表客户端已经连接，不检测客户端防火墙和运营商网络。") }}</p>
  </el-card>
  <el-descriptions :column="1" border>
    <el-descriptions-item :label='tr("服务器")'>{{ host }}</el-descriptions-item>
    <el-descriptions-item :label='tr("协议")'
      >{{ tr("普通 L2TP 或 L2TP/IPsec，接入方式由“系统设置”统一配置。") }}</el-descriptions-item
    >
    <el-descriptions-item :label='tr("账号与密码")'
      >{{ tr("在隧道映射的“编辑”中查看；停止映射后可修改。") }}</el-descriptions-item
    >
  </el-descriptions>
  <el-alert class="section-gap" :title='tr("拨号下发的 DNS：{0}。升级后重新拨号获取。", clientDNS)' type="info" :closable="false" />
  <el-collapse class="section-gap">
    <el-collapse-item :title='tr("创建与导入")' name="create"
      ><p> {{ tr("先添加 SOCKS5 节点，再新建映射并选择节点。路由表编号自动选择未被其他映射使用的值； 请确认所选路由表未被主机上的其他服务占用。填写 L2TP 账号与密码后保存并启动。批量导入格式见对应导入窗口。") }} </p></el-collapse-item
    >
    <el-collapse-item :title='tr("启动或拨号失败")' name="trouble"
      ><p> {{ tr("查看映射中的错误详情，核对 SOCKS5 地址和凭据。普通 L2TP 需放行 UDP 1701；IPsec 统一使用 NAT-T，需放行 UDP 500/4500。客户端预共享密钥须与服务器一致。SSH 中执行 s2l 可查看服务状态和日志。") }} </p></el-collapse-item
    >
    <el-collapse-item :title='tr("L2TP 客户端配置")' name="client"
      ><p> {{ tr("服务器地址填写部署 S2L 的服务器公网 IPv4 地址，用户名和密码填写对应映射的 L2TP 凭据。服务器启用 IPsec 时填写相同的预共享密钥，关闭时留空。若客户端同时配置其他 IPsec VPN，请检查预共享密钥冲突。具体兼容性需以客户端版本和实际协商结果为准。") }} </p></el-collapse-item
    >
    <el-collapse-item :title='tr("上下行测速")' name="speed"
      ><p> {{ tr("测速由服务器经所选 SOCKS5 节点访问 Cloudflare，分别显示下载、上传速度及连接延迟。 测速会消耗流量，结果不代表完整 L2TP 客户端连接的性能；各方向的失败原因单独显示。") }} </p></el-collapse-item
    >
    <el-collapse-item :title='tr("DNS 与客户端路由")' name="dns-routing">
      <p>{{ tr("接受 VPN 分配的 DNS，并确保 DNS 与需要代理的 IPv4 流量经 L2TP；服务器地址保留原网络路由。") }}</p>
      <p>{{ tr("Windows/macOS：选择客户端支持的接入模式，启用 VPN 默认路由并接受自动 DNS。Linux pppd：启用 usepeerdns，并让网络管理器或拨号脚本应用收到的 DNS。路由器：为需要代理的 LAN 流量配置 L2TP 出口和源 NAT。") }}</p>
      <p>{{ tr("如果客户端忽略 DNS 下发，请手动填写上方 DNS 地址并将它们路由进隧道。局域网 DNS、浏览器安全 DNS 和分流规则可能绕过 VPN，需分别核对。") }}</p>
      <p>{{ tr("下发地址是隧道内 DNS 请求的入口标识；实际解析器在映射高级选项配置。所有进入映射的 53 端口请求均经该映射的 SOCKS5，不会在解析失败时改为服务器直连。") }}</p>
      <p>{{ tr("客户端其他接口的 IPv6 和断线直连需在客户端阻止；服务器只能保护进入隧道的流量。") }}</p>
    </el-collapse-item>
    <el-collapse-item :title='tr("接入验收")' name="acceptance">
      <p>{{ tr("重新拨号后核对分配地址、DNS 和路由；从客户端解析域名并访问 HTTPS，确认出口属于所选 SOCKS5。需要 UDP 的应用须允许 UDP 且上游支持；面板测速不能替代客户端验证。") }}</p>
      <p>{{ tr("同一账号仅允许一条拨号会话；多设备可共享网关。") }}</p>
      <p>{{ tr("同一 NAT 下多个 IPsec 客户端可使用 L2TP 源端口 1701，服务器按会话隔离回包。Linux 双客户端已实测；原生 Windows、macOS 和路由器仍需按版本验证。每台独立拨号设备使用自己的映射账号。") }}</p>
      <p>{{ tr("允许 UDP 后，还需放行 SOCKS5 协商返回的 UDP 中继地址和端口，可能不同于代理 TCP 端口。协商成功不等于有回包；DNS 可继续经 DoH/TCP 使用固定出口。") }}</p>
      <p>{{ tr("在维护窗口验证上游中断时业务失败而不改走服务器公网出口；再验证恢复和重新拨号。普通 L2TP 需要 UDP 1701，IPsec 使用 NAT-T，需要 UDP 500/4500。仅能访问 HTTPS 的网络不能据此判断可拨号。") }}</p>
    </el-collapse-item>
  </el-collapse>
  <el-alert
    :title='tr("普通 L2TP 不加密。可选 IPsec 使用 IKEv1 与共享密钥，请确认客户端兼容性及安全策略要求。")'
    type="info"
    show-icon
    :closable="false"
  />
</template>

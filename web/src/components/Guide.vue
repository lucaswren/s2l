<script setup>
import {computed,onMounted,ref,watch} from 'vue';
import {api} from '../api/client';
const props=defineProps({mappings:{type:Array,default:()=>[]},health:{type:Object,default:()=>({})}});
const host = location.hostname;
const selected=ref('');
const settings=ref(null);
const error=ref('');
const copied=ref(false);
const mapping=computed(()=>props.mappings.find(m=>m.id===selected.value));
const local=computed(()=>props.health[selected.value]);
watch(()=>props.mappings,list=>{if(!list.some(m=>m.id===selected.value)) selected.value=(list.find(m=>m.status==='running')||list[0])?.id||'';},{immediate:true});
const readiness=computed(()=>{
  if(!mapping.value)return '请先选择映射';
  if(mapping.value.status!=='running')return '映射未运行，请先启动';
  if(!local.value)return '等待服务端链路检查';
  if(local.value.local_state!=='healthy')return '本地链路异常或恢复中，请查看运行监控';
  if(local.value.tcp?.state!=='healthy')return '本地链路已启动，上游 TCP 尚未验证正常';
  return '本地链路和上游 TCP 检查正常；仍需客户端实际拨号验证';
});
onMounted(async()=>{try{settings.value=await api.settings();}catch(e){error.value=e.message;}});
async function copy(){
  if(!mapping.value)return;
  const ipsec=settings.value?.ipsec;
  const text=`服务器：${host}（请确认是客户端可达的服务器地址）\n映射：${mapping.value.tun_name}\n用户名：${mapping.value.l2tp_user}\n接入方式：${ipsec ? (ipsec.enabled?'L2TP/IPsec':'普通 L2TP') : '请在系统设置确认'}\n密码和预共享密钥请另行填写。`;
  try{await navigator.clipboard.writeText(text);copied.value=true;error.value='';}catch{error.value='浏览器不允许复制，请手动选中连接参数';}
}
</script>
<template>
  <div class="section-head">
    <div>
      <h2>快速接入</h2>
      <p class="muted">导入节点 → 创建映射 → 启动 → 客户端拨号</p>
    </div>
  </div>
  <el-card shadow="never" class="section-gap">
    <template #header>连接准备检查</template>
    <el-select v-model="selected" placeholder="选择映射" aria-label="接入映射" @change="copied=false">
      <el-option v-for="m in mappings" :key="m.id" :value="m.id" :label="m.tun_name + ' · ' + m.l2tp_user" />
    </el-select>
    <p>{{ readiness }}</p>
    <p v-if="settings?.ipsec">IPsec：{{ settings.ipsec.enabled ? (settings.ipsec.active ? '已启用，服务活动' : '已启用，服务未活动') : '未启用' }}</p>
    <p v-else>IPsec 状态尚未获取，请在系统设置中确认。</p>
    <el-alert v-if="error" :title="error" type="warning" :closable="false" />
    <el-button :disabled="!mapping" @click="copy">{{ copied ? '连接参数已复制' : '复制连接参数（不含密码）' }}</el-button>
    <p class="muted">此检查不代表客户端已经连接，不检测客户端防火墙和运营商网络。</p>
  </el-card>
  <el-descriptions :column="1" border>
    <el-descriptions-item label="服务器">{{ host }}</el-descriptions-item>
    <el-descriptions-item label="协议"
      >普通 L2TP 或
      L2TP/IPsec，接入方式由“系统设置”统一配置。</el-descriptions-item
    >
    <el-descriptions-item label="账号与密码"
      >在隧道映射的“编辑”中查看；停止映射后可修改。</el-descriptions-item
    >
  </el-descriptions>
  <el-collapse class="section-gap">
    <el-collapse-item title="创建与导入" name="create"
      ><p>
        先添加 SOCKS5
        节点，再新建映射并选择节点。路由表编号自动选择未被其他映射使用的值；
        请确认所选路由表未被主机上的其他服务占用。填写 L2TP
        账号与密码后保存并启动。批量导入格式见对应导入窗口。
      </p></el-collapse-item
    >
    <el-collapse-item title="启动或拨号失败" name="trouble"
      ><p>
        查看映射中的错误详情，核对 SOCKS5 地址和凭据。普通 L2TP 需放行 UDP
        1701；IPsec 需放行 UDP 500、4500 和 ESP
        协议，客户端预共享密钥与服务器一致。SSH 中执行 s2l
        可查看服务状态和日志。
      </p></el-collapse-item
    >
    <el-collapse-item title="L2TP 客户端配置" name="client"
      ><p>
        服务器地址填写部署 S2L 的服务器公网 IPv4 地址，用户名和密码填写对应映射的 L2TP
        凭据。服务器启用 IPsec
        时填写相同的预共享密钥，关闭时留空。若客户端同时配置其他 IPsec
        VPN，请检查预共享密钥冲突。具体兼容性需以客户端版本和实际协商结果为准。
      </p></el-collapse-item
    >
    <el-collapse-item title="上下行测速" name="speed"
      ><p>
        测速由服务器经所选 SOCKS5 节点访问 Cloudflare，分别显示下载、上传速度及连接延迟。
        测速会消耗流量，结果不代表完整 L2TP 客户端连接的性能；各方向的失败原因单独显示。
      </p></el-collapse-item
    >
  </el-collapse>
  <el-alert
    title="普通 L2TP 不加密。可选 IPsec 使用 IKEv1 与共享密钥，请确认客户端兼容性及安全策略要求。"
    type="info"
    show-icon
    :closable="false"
  />
</template>

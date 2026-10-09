<script setup>
const host = location.hostname;
</script>
<template>
  <div class="section-head">
    <div>
      <h2>快速接入</h2>
      <p class="muted">导入节点 → 创建映射 → 启动 → 客户端拨号</p>
    </div>
  </div>
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
        节点，再新建映射并选择节点。路由表编号会自动选择空闲值，填写 L2TP
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
        服务器地址填写本机公网 IP，用户名和密码填写对应映射的 L2TP
        凭据。服务器启用 IPsec
        时填写相同的预共享密钥，关闭时留空。若客户端同时配置其他 IPsec
        VPN，请检查预共享密钥冲突。具体兼容性需以客户端版本和实际协商结果为准。
      </p></el-collapse-item
    >
    <el-collapse-item title="上下行测速" name="speed"
      ><p>
        节点页面分别显示下载和上传速度，以及 SOCKS5
        连接延迟。测速会产生流量，任一方向失败都会单独显示原因。
      </p></el-collapse-item
    >
  </el-collapse>
  <el-alert
    title="普通 L2TP 不提供加密。需要加密接入时，在系统设置中启用 IPsec。"
    type="info"
    show-icon
    :closable="false"
  />
</template>

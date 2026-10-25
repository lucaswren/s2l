// Copy only connection parameters and routing guidance, never credentials.
export function clientAccessText(host, mapping, settings, tr) {
  const mode = settings?.ipsec
    ? (settings.ipsec.enabled ? 'L2TP/IPsec' : tr("普通 L2TP"))
    : tr("请在系统设置确认");
  const dns = settings?.client_dns?.join(', ') || tr("等待服务端 DNS 参数");
  return [
    tr("服务器：{0}（请确认是客户端可达的服务器地址）\n映射：{1}\n用户名：{2}\n接入方式：{3}\n密码和预共享密钥请另行填写。", host, mapping.tun_name, mapping.l2tp_user, mode),
    tr("拨号下发的 DNS：{0}。升级后重新拨号获取。", dns),
    tr("接受 VPN 分配的 DNS，并确保 DNS 与需要代理的 IPv4 流量经 L2TP；服务器地址保留原网络路由。"),
    tr("同一账号仅允许一条拨号会话；多设备可共享网关。"),
    tr("客户端其他接口的 IPv6 和断线直连需在客户端阻止；服务器只能保护进入隧道的流量。"),
  ].join('\n');
}

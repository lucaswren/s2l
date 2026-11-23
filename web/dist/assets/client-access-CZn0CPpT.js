function u(e){return(e==null?void 0:e.available)!==!0||typeof e.enabled!="boolean"||typeof e.active!="boolean"||typeof e.protected!="boolean"?"unknown":e.enabled?e.active&&e.protected?"ipsec":"error":e.active||e.protected?"error":"plain"}function d(e,o){switch(u(e)){case"ipsec":return"L2TP/IPsec";case"plain":return o("普通 L2TP");case"error":return o("IPsec 状态异常");default:return o("请在系统设置确认")}}function t(e,o,r,n){var a;const c=d(r==null?void 0:r.ipsec,n),l=((a=r==null?void 0:r.client_dns)==null?void 0:a.join(", "))||n("等待服务端 DNS 参数");return[n(`服务器：{0}（请确认是客户端可达的服务器地址）
映射：{1}
用户名：{2}
接入方式：{3}
密码和预共享密钥请另行填写。`,e,o.tun_name,o.l2tp_user,c),n("拨号下发的 DNS：{0}。升级后重新拨号获取。",l),n("接受 VPN 分配的 DNS，并确保 DNS 与需要代理的 IPv4 流量经 L2TP；服务器地址保留原网络路由。"),n("同一账号仅允许一条拨号会话；多设备可共享网关。"),n("客户端其他接口的 IPv6 和断线直连需在客户端阻止；服务器只能保护进入隧道的流量。")].join(`
`)}export{t as a,u as b,d as c};

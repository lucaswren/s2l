import json
import sys
from http.server import ThreadingHTTPServer, SimpleHTTPRequestHandler
from pathlib import Path
from datetime import datetime, timezone
now=datetime.now(timezone.utc).isoformat()
node={"id":"n","name":"固定出口 A","addr":"192.0.2.5:1080"}
mapping={"id":"m","node_id":"n","tun_name":"tun101","table_id":101,"subnet":"10.0.101.0/24","l2tp_user":"account-a","status":"running","udp_policy":"reject","dns_mode":"auto"}
healthy={"state":"healthy","method":"https","checked_at":now,"duration_ms":123,"stage":"http","status_code":204}
status={"mappings":[mapping],"sessions":[{"interface":"ppp0","index":5,"mapping_id":"m","peer_ip":"10.0.101.2","mtu":1400,"first_seen":now,"rx_bytes":524288,"tx_bytes":125000,"dropped":0,"errors":0}],"upstream_health":{"m":{"state":"healthy","tun_name":"tun101","udp_policy":"reject","local_state":"healthy","checked_at":now,"tcp":healthy,"dns":dict(healthy,method="doh"),"udp":dict(healthy,state="unsupported",method="udp-associate"),"resolver":{"active":"doh"}}},"singbox_pids":{"tun101":100},"tun_traffic":[{"tun_name":"tun101","rx_bytes":10240,"tx_bytes":20480}]}
class Handler(SimpleHTTPRequestHandler):
 def __init__(self,*args,**kwargs):super().__init__(*args,directory=str((Path(__file__).resolve().parents[1] / 'web/dist')),**kwargs)
 def log_message(self,*args):pass
 def reply(self,data):
  body=json.dumps(data).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
 def do_GET(self):
  if self.path=='/api/settings/ppp-management':return self.reply(dict(available=True,access=dict(enabled=False,destination_ip='',source_cidrs=[],tcp_ports=[]),sources=[dict(id='m',subnet='10.0.101.0/24',name='account-a')],detected_ports=[38866,51234],configured_ports=[38866,51234],port_check_available=True,local_ipv4=['156.245.235.230']))
  if self.path=='/api/settings':return self.reply(dict(account_enabled=False,ssh_enabled=False,ssh_ports=[51234],ipsec=dict(available=False)))
  if self.path=='/api/settings/update':return self.reply(dict(available=False,current=dict(version='development')))
  if self.path=='/api/operations':return self.reply({'events':[{'id':1,'kind':'incident-open','resource':'m','detail':'故障开始','at':now}],'incidents':[{'key':'node:n','state':'upstream','scope':'node','affected':['m'],'started':now,'resolved':'0001-01-01T00:00:00Z'}],'enabled':False,'configured':False,'revision':'fixture-r1'})
  if self.path.endswith('/connections'):return self.reply({'rows':[{'id':'c1','metadata':{'host':'example.com','destinationPort':'443','network':'tcp'},'upload':1024,'download':2048,'start':now}]})
  if self.path=='/api/nodes':return self.reply([node,dict(node,id='n2',name='对照出口 B')])
  if self.path=='/api/mappings':return self.reply([mapping])
  if self.path=='/api/status':return self.reply(status)
  if self.path.startswith('/api/history'):return self.reply({'buckets':[{'at':int(datetime.now().timestamp())//300*300,'tcp':{'pass':8,'fail':1,'restricted':1},'dns':{'pass':10},'latency_total_ms':800,'latency_samples':8,'repairs':1,'restarts':0,'session_changes':1}],'warning':''})
  return super().do_GET()
 def do_POST(self):
  body=json.loads(self.rfile.read(int(self.headers.get('Content-Length','0'))) or '{}')
  if self.path=='/api/settings/ppp-management':return self.reply(dict(access=body,message='PPP 管理访问已更新'))
  if body.get('kind')=='google':
   def report(n):
    dns={'state':'healthy','addresses':['203.0.113.5'],'duration_ms':5,'rcode':0}
    hosts=['www.google.com','accounts.google.com','oauth2.googleapis.com','gmail.googleapis.com','www.youtube.com','gemini.google.com','robinfrontend-pa.googleapis.com','oauthaccountmanager.googleapis.com']
    rows=[dict(host=h,a=dns,aaaa=dict(dns,state='no_address',addresses=[]),doh=dns,tcp_dns=dns,tcp='healthy',tls='healthy',http='responded',status=403,alpn='h2',stage='http',duration_ms=100) for h in hosts]
    if n=='n':rows[3].update(tls='unavailable',http='not_tested',status=0,stage='tls')
    return dict(node_id=n,node_name='固定出口 A' if n=='n' else '对照出口 B',checked_at=now,targets=rows,exit=dict(healthy,exit_ip='203.0.113.5'),scope='服务器 → 固定 SOCKS5，不经过手机/Panabit',quic='未发送 UDP/QUIC；不证明 iPhone 已回退。')
   return self.reply(dict(kind='google',schema_version=1,ok=False,google=[report('n')]+([report('n2')] if body.get('compare_node') else []),mapping_policies=[dict(mapping_id='m',node_id='n',udp_policy='reject',dns_mode='auto',max_connections=0,max_sessions=0)]))
  if self.path=='/api/mappings?preview=1':return self.reply({'fields':['l2tp_pass','upload_kbps','download_kbps'],'affected':['m'],'message':'保存前已校验，测试配置预览。'})
  if self.path.startswith('/api/mappings/'):
   return self.reply({'ok':True,'rules':healthy,'forwarding':dict(healthy,exit_ip='203.0.113.5'),'node':dict(healthy,exit_ip='203.0.113.5'),'exit_comparison':'same','scope':'PPP 模拟入口 → 现有策略路由与防火墙 → TUN → 固定 SOCKS5；不包含 L2TP 认证'})
  return self.reply({'kind':body.get('kind'),'ok':False,'protocols':{'tcp':dict(healthy,state='restricted',status_code=403,error='目标已响应 HTTP 403')},'target':body.get('target')})
if __name__ == '__main__':
 ThreadingHTTPServer(('127.0.0.1',int(sys.argv[1])),Handler).serve_forever()

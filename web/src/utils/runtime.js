// All rates use server collection timestamps, never the browser polling period.
export function normalizeStatus(value, mappings = []) {
 const st=value && typeof value==='object' ? value : {};
 return {...st,uptime_sec:st.uptime_sec||0,build:st.build||{},
  tun_traffic:Array.isArray(st.tun_traffic)?st.tun_traffic:[],singbox_pids:st.singbox_pids||{},
  upstream_health:st.upstream_health||{},host_stats:st.host_stats||{},
  mappings:Array.isArray(st.mappings)?st.mappings:mappings,
  sessions:Array.isArray(st.sessions)?st.sessions:[],sessions_error:st.sessions_error||'',
  sessions_checked_at:st.sessions_checked_at||'',collected_at:st.collected_at||'',
  configuration_recovery:st.configuration_recovery||''};
}
export function validSampleTime(value) {
 if(typeof value!=='string'||value.startsWith('0001-'))return null;
 const n=Date.parse(value);return Number.isFinite(n)?n:null;
}
export function sampleRuntime(previous,current) {
 const start=validSampleTime(previous?.collected_at),end=validSampleTime(current?.collected_at);
 const seconds=start!==null&&end!==null?(end-start)/1000:0;
 const valid=seconds>0&&seconds<=30&&Number.isFinite(previous?.uptime_sec)&&Number.isFinite(current?.uptime_sec)&&current.uptime_sec>=previous.uptime_sec;
 const delta=(a,b)=>valid&&Number.isSafeInteger(a)&&Number.isSafeInteger(b)&&a>=b?a-b:null;
 const sessions={};
 const peerStart=validSampleTime(previous?.sessions_checked_at),peerEnd=validSampleTime(current?.sessions_checked_at);
 const peerSeconds=peerStart!==null&&peerEnd!==null?(peerEnd-peerStart)/1000:0;
 for(const row of current?.sessions||[]) {
  const old=previous?.sessions?.find(p=>p.index===row.index&&p.interface===row.interface&&p.peer_ip===row.peer_ip&&p.mapping_id===row.mapping_id&&p.connected_at===row.connected_at);
  // A failed session read retains an old snapshot; it must never imply zero traffic.
  const usable=old&&!current.sessions_error&&!previous.sessions_error&&peerSeconds>0&&peerSeconds<=30;
  const up=usable?delta(row.rx_bytes,old.rx_bytes):null,down=usable?delta(row.tx_bytes,old.tx_bytes):null;
  sessions[row.index]={upload:up===null?null:up*8/peerSeconds/1e6,download:down===null?null:down*8/peerSeconds/1e6};
 }
 const processes={};
 for(const [id,h] of Object.entries(current?.upstream_health||{})) {
  const old=previous?.upstream_health?.[id],pid=current.singbox_pids?.[h.tun_name];
  const same=pid>0&&pid===previous?.singbox_pids?.[h.tun_name]&&h.process?.started_at===old?.process?.started_at;
  processes[id]=same?delta(h.process?.error_lines,old?.process?.error_lines):null;
 }
 const retrans=delta(current?.host_stats?.RetransSegs,previous?.host_stats?.RetransSegs);
 return {sessions,processes,host:{retrans},seconds:valid?seconds:null};
}
export function mbps(value) { return Number.isFinite(value)&&value>=0?`${value.toFixed(2)} Mbps`:'—'; }

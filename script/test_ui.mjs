import {spawn} from 'node:child_process';
import {writeFile,mkdir,mkdtemp} from 'node:fs/promises';
import {createServer} from 'node:net';
import {fileURLToPath} from 'node:url';
const cwd=fileURLToPath(new URL('../',import.meta.url)), pause=ms=>new Promise(r=>setTimeout(r,ms));
await mkdir(cwd+'/tmp',{recursive:true});
async function freePort(){return new Promise((ok,no)=>{const s=createServer();s.on('error',no);s.listen(0,'127.0.0.1',()=>{const p=s.address().port;s.close(()=>ok(p))})})}
const apiPort=await freePort(),cdpPort=await freePort(),profile=await mkdtemp(cwd+'/tmp/ui-chrome-');
const server=spawn(process.env.S2L_PYTHON_BIN || (process.platform==='win32'?'python':'python3'),['script/ui_fixture.py',String(apiPort)],{cwd,windowsHide:true,stdio:'ignore'});
const chrome=spawn(process.env.S2L_CHROME_BIN || (process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':'google-chrome'),['--headless=new','--disable-gpu','--no-first-run',`--remote-debugging-port=${cdpPort}`,`--user-data-dir=${profile}`,'about:blank'],{windowsHide:true,stdio:'ignore'});
let socket;
try{
 let pages;for(let i=0;i<60;i++){try{pages=await(await fetch(`http://127.0.0.1:${cdpPort}/json`)).json();if(pages.length)break}catch{}await pause(150)}
 if(!pages?.length)throw Error('headless browser unavailable');
 for(let i=0;i<60;i++){try{if((await fetch(`http://127.0.0.1:${apiPort}/api/nodes`)).ok)break}catch{}await pause(100)}
 const target=await (await fetch(`http://127.0.0.1:${cdpPort}/json/new?about:blank`,{method:'PUT'})).json();
 socket=new WebSocket(target.webSocketDebuggerUrl);await new Promise((ok,no)=>{socket.onopen=ok;socket.onerror=no});
 let seq=0;const pending=new Map(),errors=[];
 socket.onmessage=e=>{const d=JSON.parse(e.data);if(d.id){const p=pending.get(d.id);if(p){pending.delete(d.id);d.error?p.reject(Error(d.error.message)):p.resolve(d.result)}}else if(d.method==='Runtime.exceptionThrown')errors.push(d.params.exceptionDetails.text)};
 function send(method,params={}){return new Promise((resolve,reject)=>{const id=++seq;pending.set(id,{resolve,reject});socket.send(JSON.stringify({id,method,params}));setTimeout(()=>{if(pending.has(id)){pending.delete(id);reject(Error('CDP timeout '+method))}},10000).unref()})}
 async function evaluate(expression){const r=await send('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});if(r.exceptionDetails)throw Error(JSON.stringify(r.exceptionDetails));return r.result.value}
 async function wait(expression){for(let i=0;i<70;i++){if(await evaluate(expression))return;await pause(100)}throw Error('UI readiness timeout: '+expression+' BODY='+await evaluate('document.documentElement.outerHTML.slice(0,2500)')+' ERRORS='+JSON.stringify(errors))}
 await send('Page.enable');await send('Runtime.enable');
 const results=[];
 for(const width of [1440,390,320]){
  await send('Emulation.setDeviceMetricsOverride',{width,height:900,deviceScaleFactor:1,mobile:width<600});
  await send('Page.navigate',{url:`http://127.0.0.1:${apiPort}/`});
  await wait(`!!document.querySelector('.el-collapse-item__header') && document.body.innerText.includes('固定出口 A')`);
  await evaluate(`[...document.querySelectorAll('button')].find(b=>b.offsetParent && b.innerText==='新建映射').click()`);
  await wait(`!!document.querySelector('.el-dialog input[type=password]')`);
  await evaluate(`(()=>{const p=document.querySelector('.el-dialog input[type=password]');p.value='fixture-password';p.dispatchEvent(new Event('input',{bubbles:true}));})()`);
  await evaluate(`[...document.querySelectorAll('.el-dialog button')].find(b=>b.innerText==='保存').click()`);
  await wait(`!!document.querySelector('.el-message-box') && document.querySelector('.el-message-box').innerText.includes('变更：')`);
  await pause(650);
  const previewBounds=await evaluate(`(()=>{const r=document.querySelector('.el-message-box').getBoundingClientRect();return {left:r.left,right:r.right,viewport:innerWidth}})()`);
  if(previewBounds.left<0||previewBounds.right>previewBounds.viewport+2)throw Error('configuration preview overflow '+width);
  await evaluate(`[...document.querySelectorAll('.el-message-box button')].find(b=>b.innerText==='返回编辑').click()`);await pause(350);
  await evaluate(`document.querySelector('.el-dialog .el-dialog__headerbtn').click()`);await pause(350);
  await evaluate(`document.querySelector('.el-collapse-item__header').click()`);
  await wait(`document.body.innerText.includes('首次观测') && document.body.innerText.includes('80.0%')`);
  await wait(`document.body.innerText.includes('故障事件与操作记录') && document.body.innerText.includes('节点转发异常 · 处理中')`);
  await evaluate(`[...document.querySelectorAll('button')].find(b=>b.offsetParent && b.innerText.includes('查看连接')).click()`);
  await wait(`document.querySelector('.connections-dialog')?.innerText.includes('example.com')`);
  await pause(650);
  let connectionBounds=await evaluate(`(()=>{const r=document.querySelector('.connections-dialog').getBoundingClientRect();return {left:r.left,right:r.right,viewport:innerWidth}})()`);
  if(connectionBounds.left<0||connectionBounds.right>connectionBounds.viewport+2)throw Error('connection dialog overflow '+width);
  await send('Page.captureScreenshot',{format:'png'}).then(r=>writeFile(`${cwd}/tmp/ui-connections-${width}.png`,Buffer.from(r.data,'base64')));
  await evaluate(`window.__closeRequests=0;window.__beforeFetch=window.fetch;window.fetch=(path,options)=>{if(String(path).endsWith('/connections')&&options?.method==='POST')window.__closeRequests++;return window.__beforeFetch(path,options)};[...document.querySelectorAll('.connections-dialog button')].find(b=>b.innerText==='关闭连接').click()`);
  await wait(`!!document.querySelector('.el-message-box')`);
  await evaluate(`document.querySelector('.connections-dialog .el-dialog__headerbtn').click()`);await pause(350);
  await evaluate(`[...document.querySelectorAll('.el-message-box button')].find(b=>b.classList.contains('el-button--primary')).click()`);await pause(350);
  if(await evaluate(`window.__closeRequests!==0`))throw Error('closed dialog still closed a connection '+width);
  await evaluate(`window.fetch=window.__beforeFetch`);
  await evaluate(`[...document.querySelectorAll('.operations-panel .el-collapse-item__header')].find(b=>b.innerText.includes('故障通知')).click()`);await pause(350);
  await evaluate(`window.__notificationPosts=0;window.__beforeFetch=window.fetch;window.fetch=(path,options)=>{if(path==='/api/operations'&&options?.method==='POST'){window.__notificationPosts++;window.__notificationBody=JSON.parse(options.body);return new Promise(resolve=>setTimeout(()=>resolve(Response.json({events:[],incidents:[],enabled:false,configured:false,revision:'fixture-r2'})),450))}return window.__beforeFetch(path,options)};const save=[...document.querySelectorAll('.operations-panel button')].find(b=>b.innerText==='保存通知设置');save.click();save.click()`);
  await pause(650);
  if(await evaluate(`window.__notificationPosts!==1 || window.__notificationBody.revision!=='fixture-r1'`))throw Error('notification save concurrency/revision '+width);
  await evaluate(`window.fetch=window.__beforeFetch`);

  let overflow=await evaluate(`document.documentElement.scrollWidth>innerWidth+2`);if(overflow)throw Error('horizontal overflow '+width);
  await evaluate(`[...document.querySelectorAll('button')].find(b=>b.offsetParent && b.innerText==='映射转发实测').click()`);
  await wait(`document.body.innerText.includes('映射转发正常')`);
  await pause(650);
  await send('Page.captureScreenshot',{format:'png'}).then(r=>writeFile(`${cwd}/tmp/ui-mapping-${width}.png`,Buffer.from(r.data,'base64')));
  await evaluate(`document.querySelector('.mapping-diagnostic .el-dialog__headerbtn').click()`);await pause(350);
  await evaluate(`[...document.querySelectorAll('button')].find(b=>b.offsetParent && b.innerText==='网络测试').click()`);
  await wait(`!!document.querySelector('.network-test-dialog')`);
  await evaluate(`document.querySelector('.network-test-modes .el-select').click()`);await wait(`[...document.querySelectorAll('.el-select-dropdown__item')].some(e=>e.innerText==='HTTPS')`);await evaluate(`[...document.querySelectorAll('.el-select-dropdown__item')].find(e=>e.innerText==='HTTPS').click()`);
  await evaluate(`[...document.querySelectorAll('.network-test-dialog button')].find(b=>b.innerText==='开始测试').click()`);
  await wait(`document.querySelector('.network-test-dialog').innerText.includes('HTTP 403')`);
  await pause(650);
  await send('Page.captureScreenshot',{format:'png'}).then(r=>writeFile(`${cwd}/tmp/ui-network-${width}.png`,Buffer.from(r.data,'base64')));
  const bounds=await evaluate(`(()=>{const r=document.querySelector('.network-test-dialog').getBoundingClientRect();return {left:r.left,right:r.right,viewport:innerWidth}})()`);
  if(bounds.left<0||bounds.right>bounds.viewport+2)throw Error('dialog overflow '+width);
  await evaluate(`document.querySelector('.network-test-modes .el-select').click()`);await wait(`[...document.querySelectorAll('.el-select-dropdown__item')].some(e=>e.innerText==='Google 专项')`);await evaluate(`[...document.querySelectorAll('.el-select-dropdown__item')].find(e=>e.innerText==='Google 专项').click()`);
  await evaluate(`document.querySelector('.network-test-options .el-select').click()`);
  await wait(`[...document.querySelectorAll('.el-select-dropdown__item')].some(e=>e.innerText==='对照出口 B')`);
  await evaluate(`[...document.querySelectorAll('.el-select-dropdown__item')].find(e=>e.innerText==='对照出口 B').click()`);
  await evaluate(`[...document.querySelectorAll('.network-test-dialog button')].find(b=>b.innerText==='开始测试').click()`);
  await wait(`!!document.querySelector('.google-comparison') && document.querySelector('.google-diagnostic').innerText.includes('阶段差异：tls')`);
  if(await evaluate(`document.querySelectorAll('.google-target').length!==16`))throw Error('Google comparison missing targets '+width);
  await evaluate(`document.querySelector('.google-tools summary').click()`);
  await evaluate(`(async()=>{const d=await(await fetch('/api/nodes/n/network-test',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({kind:'google'})})).json();d.google[0].node_name='历史正常线路';const dt=new DataTransfer();dt.items.add(new File([JSON.stringify(d)],'google.json',{type:'application/json'}));const input=document.querySelector('.google-diagnostic input[type=file]');input.files=dt.files;input.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  await wait(`document.querySelector('.google-diagnostic').innerText.includes('历史对照：历史正常线路')`);
  const googleBounds=await evaluate(`(()=>{const e=document.querySelector('.network-test-dialog .el-dialog__body');return {width:e.clientWidth,scroll:e.scrollWidth}})()`);
  if(googleBounds.scroll>googleBounds.width+2)throw Error('Google diagnostic overflow '+width+' '+JSON.stringify(await evaluate(`(()=>{const b=document.querySelector('.network-test-dialog .el-dialog__body'),r=b.getBoundingClientRect();return [...b.querySelectorAll('*')].filter(e=>e.getBoundingClientRect().right>r.right+1).slice(0,12).map(e=>({tag:e.tagName,cls:e.className,width:e.clientWidth,scroll:e.scrollWidth}))})()`)));
  await send('Page.captureScreenshot',{format:'png'}).then(r=>writeFile(`${cwd}/tmp/ui-google-${width}.png`,Buffer.from(r.data,'base64')));
  await evaluate(`document.querySelector('.network-test-dialog .el-dialog__headerbtn').click()`);await pause(350);
  await evaluate(`[...document.querySelectorAll('[role=tab]')].find(e=>e.innerText==='系统设置').click()`);
  await wait(`!!document.querySelector('details[aria-label="远程管理"] > summary')`);
  await evaluate(`document.querySelector('details[aria-label="远程管理"] > summary').click()`);
  await wait(`!!document.querySelector('.ppp-management') && document.querySelector('.ppp-management').innerText.includes('51234')`);
  await evaluate(`(()=>{const card=document.querySelector('.ppp-management');card.querySelector('.el-switch').click();const inputs=card.querySelectorAll('input');const ip=[...inputs].find(e=>e.placeholder.includes('公网'));ip.value='156.245.235.230';ip.dispatchEvent(new Event('input',{bubbles:true}));const subnet=card.querySelector('textarea');subnet.value='10.0.101.0/24';subnet.dispatchEvent(new Event('input',{bubbles:true}));[...card.querySelectorAll('button')].find(b=>b.innerText==='填入检测到的面板及 SSH 端口').click()})()`);
  await evaluate(`window.__managementPosts=0;window.__beforeManagementFetch=window.fetch;window.fetch=(path,options)=>{if(path==='/api/settings/ppp-management'&&options?.method==='POST'){window.__managementPosts++;window.__managementBody=JSON.parse(options.body);return new Promise(resolve=>setTimeout(()=>resolve(Response.json({access:window.__managementBody,message:'PPP 管理访问已更新'})),250))}return window.__beforeManagementFetch(path,options)};const b=[...document.querySelectorAll('.ppp-management button')].find(e=>e.innerText==='保存管理例外');b.click();b.click()`);
  await wait(`document.querySelector('.ppp-management').innerText.includes('PPP 管理访问已更新')`);
  if(await evaluate(`window.__managementPosts!==1||!window.__managementBody.enabled||window.__managementBody.tcp_ports.join(',')!=='38866,51234'||window.__managementBody.source_cidrs[0]!=='10.0.101.0/24'`))throw Error('management save payload/concurrency '+width);
  await evaluate(`window.fetch=window.__beforeManagementFetch`);
  const managementBounds=await evaluate(`(()=>{const e=document.querySelector('.ppp-management');return {width:e.clientWidth,scroll:e.scrollWidth}})()`);if(managementBounds.scroll>managementBounds.width+2)throw Error('management card overflow '+width);
  await send('Page.captureScreenshot',{format:'png'}).then(r=>writeFile(`${cwd}/tmp/ui-management-${width}.png`,Buffer.from(r.data,'base64')));
  results.push({width,overflow:false,sessions:true,history:true,mappingDiagnostic:true,httpsRestriction:true,incidents:true,connections:true,configurationPreview:true,notificationSaveGuard:true,closedConnectionGuard:true,googleComparison:true,googleSnapshot:true,managementException:true});
 }
 if(errors.length)throw Error(errors.join('\n'));
 console.log(JSON.stringify({results,consoleErrors:errors}));
 await send('Browser.close');
}finally{socket?.close();server.kill();chrome.kill()}

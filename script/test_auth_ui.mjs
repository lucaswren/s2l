// Real backend authentication and embedded UI, with disposable loopback data.
import {spawn,execFileSync} from 'node:child_process';
import {writeFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {pause,freePort,createFixtureDir,disposeFixture} from './ui_test_helpers.mjs';
const cwd=fileURLToPath(new URL('../',import.meta.url));
const htmlLanguage=value=>({'zh-CN':'zh-Hans','zh-TW':'zh-Hant'}[value]||value);
const httpPort=await freePort(),cdpPort=await freePort(),dir=await createFixtureDir(cwd,'auth-ui-');
const binary=dir+(process.platform==='win32'?'/api.test.exe':'/api.test');
let backend,chrome,socket;
try {
  execFileSync('go',['test','-c','-o',binary,'./internal/api'],{cwd,windowsHide:true,stdio:'inherit'});
  backend=spawn(binary,['-test.run','^TestAuthBrowserFixture$','-test.timeout','5m'],{cwd,env:{...process.env,TMPDIR:dir,TEMP:dir,TMP:dir,S2L_AUTH_UI_FIXTURE:`127.0.0.1:${httpPort}`},windowsHide:true,stdio:'ignore'});
  let backendError;
  backend.on('error',error=>{backendError=error});
  chrome=spawn(process.env.S2L_CHROME_BIN||(process.platform==='win32'?'C:/Program Files/Google/Chrome/Application/chrome.exe':'google-chrome'),['--headless=new','--disable-gpu','--no-first-run',`--remote-debugging-port=${cdpPort}`,`--user-data-dir=${dir}/profile`,'about:blank'],{cwd,windowsHide:true,stdio:['ignore','ignore','pipe']});
  let chromeLog='',chromeError;
  chrome.stderr.on('data',data=>{chromeLog=(chromeLog+data.toString()).slice(-6000)});
  chrome.on('error',error=>{chromeError=error});
  let pages;
  for(let i=0;i<300;i++){if(backendError)throw backendError;if(chromeError||chrome.exitCode!==null)throw Error('browser exited before readiness: '+(chromeError||chrome.exitCode)+' '+chromeLog);try{pages=await(await fetch(`http://127.0.0.1:${cdpPort}/json`)).json();if(pages.length&&(await fetch(`http://127.0.0.1:${httpPort}/api/auth/status`)).ok)break}catch{}await pause(100)}
  if(!pages?.length)throw Error('authentication browser fixture unavailable after 30s: '+chromeLog);
  const target=await(await fetch(`http://127.0.0.1:${cdpPort}/json/new?about:blank`,{method:'PUT'})).json();
  socket=new WebSocket(target.webSocketDebuggerUrl);await new Promise((resolve,reject)=>{socket.onopen=resolve;socket.onerror=reject});
  let seq=0;const pending=new Map(),errors=[];
  socket.onmessage=event=>{const data=JSON.parse(event.data);if(data.id){const p=pending.get(data.id);if(p){pending.delete(data.id);data.error?p.reject(Error(data.error.message)):p.resolve(data.result)}}else if(data.method==='Runtime.exceptionThrown')errors.push(data.params.exceptionDetails.text)};
  function send(method,params={}){return new Promise((resolve,reject)=>{const id=++seq;pending.set(id,{resolve,reject});socket.send(JSON.stringify({id,method,params}));setTimeout(()=>{if(pending.delete(id))reject(Error('CDP timeout: '+method))},10000).unref()})}
  async function evaluate(expression){const value=await send('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});if(value.exceptionDetails)throw Error(JSON.stringify(value.exceptionDetails));return value.result.value}
  async function wait(expression){for(let i=0;i<80;i++){if(await evaluate(expression))return;await pause(100)}throw Error('UI timeout: '+expression+' '+await evaluate('document.body.innerText.slice(0,1000)'))}
  await send('Page.enable');await send('Runtime.enable');await send('Network.enable');
  const results=[];
  for(const width of [1440,390,320]) {
    await send('Emulation.setDeviceMetricsOverride',{width,height:900,deviceScaleFactor:1,mobile:width<600});
    await send('Page.navigate',{url:`http://127.0.0.1:${httpPort}/`});
    await wait(`!!document.querySelector('#login-user')`);
    for(const[language,login,settings]of [['en','Sign in','Settings'],['zh-TW','登入','系統設置'],['zh-CN','登录','系统设置']]) {
      await evaluate(`(()=>{const s=document.querySelector('.language-selector');s.value=${JSON.stringify(language)};s.dispatchEvent(new Event('change',{bubbles:true}))})()`);
      await wait(`document.documentElement.lang===${JSON.stringify(htmlLanguage(language))}&&document.querySelector('.login-submit').innerText===${JSON.stringify(login)}`);
      if(await evaluate(`document.documentElement.scrollWidth>innerWidth+2`))throw Error('login overflow '+width+' '+language);
      const controlSizes=await evaluate(`(()=>{const b=document.querySelector('.login-submit'),u=document.querySelector('.login-card .el-input__wrapper');return [b.getBoundingClientRect().height,u.getBoundingClientRect().height,getComputedStyle(b).fontSize]})()`);
      if(controlSizes[0]!==32||controlSizes[1]!==32||controlSizes[2]!=='14px')throw Error('nonstandard login controls '+JSON.stringify(controlSizes));
      await evaluate(`(()=>{for(const[id,value]of [['login-user','fixture-admin'],['login-password','wrong']]){const e=document.getElementById(id);e.value=value;e.dispatchEvent(new Event('input',{bubbles:true}))}document.querySelector('.login-submit').click()})()`);
      await wait(`!!document.querySelector('.login-card .el-alert')`);
      if(await evaluate(`!!document.querySelector('.workspace')`))throw Error('bad password opened management');
      await evaluate(`(()=>{const p=document.querySelector('#login-password');p.value='fixture-password-123';p.dispatchEvent(new Event('input',{bubbles:true}));document.querySelector('.login-submit').click()})()`);
      await wait(`!!document.querySelector('.workspace')`);
      if(!await evaluate(`document.body.innerText.includes(${JSON.stringify(settings)})`))throw Error('management locale '+language);
      if(await evaluate(`document.cookie.includes('s2l_session')||JSON.stringify(localStorage).includes('fixture-password')||JSON.stringify(sessionStorage).includes('fixture-password')`))throw Error('session or password exposed to scripts');
      const secure=await evaluate(`(async()=>{const r=await fetch('/api/nodes',{});return {ok:r.ok,popup:r.headers.get('WWW-Authenticate')}})()`);
      if(!secure.ok||secure.popup)throw Error('cookie authentication failed');
      if(await evaluate(`document.documentElement.scrollWidth>innerWidth+2`))throw Error('management language overflow '+width+' '+language);
      await send('Page.captureScreenshot',{format:'png'}).then(r=>writeFile(`${cwd}/tmp/ui-auth-${language}-${width}.png`,Buffer.from(r.data,'base64')));
      for(let i=0;i<40;i++) {
        if(await evaluate(`(async()=>{const s=await(await fetch('/api/auth/status')).json();return s.language===${JSON.stringify(language)}})()`))break;
        if(i===39)throw Error('server language was not persisted '+language);
        await pause(100);
      }
      // Clearing browser storage must recover the server-side language.
      await evaluate(`localStorage.removeItem('s2l-language')`);
      // Reload retains a valid server-side session, including language selection.
      await send('Page.reload');await wait(`!!document.querySelector('.workspace')`);
      if(!await evaluate(`document.documentElement.lang===${JSON.stringify(htmlLanguage(language))}`))throw Error('language persistence failed');
      if(await evaluate(`document.querySelectorAll('.hero .el-button').length!==2 || !document.querySelector('.toolbar-buttons').lastElementChild.classList.contains('header-signout')`))throw Error('header sign-out missing or misplaced');
      await evaluate(`document.querySelectorAll('[role=tab]')[1].click()`);
      await wait(`!!document.querySelector('.system-settings')`);
      await evaluate(`document.querySelector('.system-settings details').open=true`);
      const signOutLabel={en:'Sign out','zh-TW':'登出','zh-CN':'退出登录'}[language];
      await wait(`[...document.querySelectorAll('.hero button')].some(e=>e.innerText===${JSON.stringify(signOutLabel)})`);
      await evaluate(`[...document.querySelectorAll('.hero button')].find(e=>e.innerText===${JSON.stringify(signOutLabel)}).click()`);

      await wait(`!!document.querySelector('#login-user')`);
      await send('Page.reload');await wait(`!!document.querySelector('#login-user')`);
      results.push({width,language,login:true,logout:true,cookie:true,overflow:false});
    }
  }
  if(errors.length)throw Error(errors.join('\n'));
  console.log(JSON.stringify({results,consoleErrors:errors}));
  await send('Browser.close');
} finally {socket?.close();await disposeFixture(cwd,dir,[backend,chrome])}

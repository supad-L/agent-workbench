const {app,BrowserWindow,ipcMain,dialog,safeStorage,nativeImage,shell,nativeTheme}=require('electron');
const path=require('node:path');const fs=require('node:fs');const {pathToFileURL}=require('node:url');const {spawn}=require('node:child_process');const readline=require('node:readline');const {setTimeout:delay}=require('node:timers/promises');const {ConnectionStore}=require('./connections.cjs');
const {CodexAccount,claudeReadiness}=require('./native-accounts.cjs');
const {applyAppearance}=require('./appearance.cjs');
app.setName('Agent Workbench');
const root=path.resolve(__dirname,'..');
const argument=process.argv.find(value=>value.startsWith('--data-dir='));
const dataDir=argument?path.resolve(argument.slice('--data-dir='.length)):(process.env.AWB_DESKTOP_DATA_DIR||(!app.isPackaged?path.join(root,'data','desktop'):app.getPath('userData')));
app.setPath('userData',dataDir);
let backend,endpoint,humanToken,window,stream,quitting=false;let connections;
let codexAccount;
const indexFile=path.join(root,'web','dist','index.html');const indexURL=pathToFileURL(indexFile).href;
// This checks the application's own startup handshake, without UI automation.
const smokeCheck=process.argv.includes('--smoke-check');
const startupOperations=new Set();let loaded=false,startupReported=false,smokeTimer;
function checkStartup(){
 if(!smokeCheck||startupReported||!loaded||!startupOperations.has('meta')||!startupOperations.has('projects.list'))return;
 startupReported=true;
 clearTimeout(smokeTimer);
 process.stdout.write(JSON.stringify({check:'desktop_startup',status:'passed',packaged:app.isPackaged,renderer_ready:true,backend_health:'ok'})+'\n');
 app.quit();
}

async function startBackend(){
 const binary=app.isPackaged?path.join(process.resourcesPath,'bin','agent-workbench'):path.join(root,'bin','agent-workbench');
 if(!fs.existsSync(binary))throw new Error('后端尚未构建，请运行 python3 scripts/dev.py build');
 backend=spawn(binary,['serve','--data-dir',path.join(dataDir,'state')],{stdio:['ignore','pipe','pipe'],windowsHide:true});
 let safeError='';backend.stderr.on('data',chunk=>{safeError=(safeError+chunk.toString()).slice(-2048);});
 await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(new Error('本地服务启动超时')),15000);const reader=readline.createInterface({input:backend.stdout});reader.once('line',line=>{clearTimeout(timer);try{const ready=JSON.parse(line);if(!/^http:\/\/127\.0\.0\.1:\d+$/.test(ready.url)||typeof ready.human_token!=='string')throw new Error();endpoint=ready.url;humanToken=ready.human_token;resolve();}catch{reject(new Error('本地服务返回了无效的启动信息'));}});backend.once('error',error=>{clearTimeout(timer);reject(new Error('无法启动本地服务：'+error.code));});backend.once('exit',()=>{clearTimeout(timer);reject(new Error(safeError||'本地服务已退出'));});});
 backend.on('exit',()=>{if(!quitting&&window){if(smokeCheck)process.stderr.write('本地服务已停止\n');else dialog.showErrorBox('本地服务已停止','请重新打开工作台，已有数据会保留。');app.quit();}});
}
async function request(method,route,body,signal){
 const response=await fetch(endpoint+route,{method,headers:{Authorization:'Bearer '+humanToken,'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body),signal});
 const value=await response.json();if(!response.ok)throw new Error(value.error||`本地请求失败 (${response.status})`);return value;
}
function projectID(payload){if(typeof payload.id!=='string'||!/^[A-Za-z0-9_-]{1,64}$/.test(payload.id))throw new Error('项目编号无效');return encodeURIComponent(payload.id);}
function stopEvents(){if(stream)stream.abort();stream=null;}
async function syncConnections(){
 const entries=await connections.list();
 for(const entry of entries)await request('PUT','/api/connections/'+encodeURIComponent(entry.id),connections.projection(entry));
 return entries;
}
async function runValues(choices){
 const values={},revisions={};
 for(const choice of choices){const id=choice.connection_id||choice.connection?.id;const revision=choice.connection?.revision;const summaries=await connections.list();const current=summaries.find(entry=>entry.id===id);if(!current)throw new Error('角色引用的连接不存在');const resolved=await connections.resolve(id,revision||current.revision);values[id]=resolved.values;revisions[id]=resolved.summary.revision;}
 return {values,revisions};
}
function startEvents(id,sender){stopEvents();const controller=new AbortController();stream=controller;let cursor=0;(async()=>{while(!controller.signal.aborted){try{
 const response=await fetch(`${endpoint}/api/projects/${id}/events?cursor=${cursor}`,{headers:{Authorization:'Bearer '+humanToken},signal:controller.signal});if(!response.ok)throw new Error('事件连接失败');let buffer='';const decoder=new TextDecoder();for await(const part of response.body){buffer+=decoder.decode(part,{stream:true});if(buffer.length>1048576)throw new Error('事件帧过大');let index;while((index=buffer.indexOf('\n\n'))!==-1){const frame=buffer.slice(0,index);buffer=buffer.slice(index+2);const data=frame.split('\n').find(line=>line.startsWith('data: '));if(data){const event=JSON.parse(data.slice(6));if(event.sequence>cursor){cursor=event.sequence;if(!sender.isDestroyed())sender.send('workbench:event',event);}}}}
 }catch{if(controller.signal.aborted)break;}try{await delay(600,undefined,{signal:controller.signal});}catch{break;}}})();return {subscribed:true};}

async function invoke(operation,payload,sender){
 if(Buffer.byteLength(JSON.stringify(payload||{}))>1048576)throw new Error('操作内容过大');
 switch(operation){
 case 'appearance.set':return applyAppearance(payload.source,{nativeTheme,window});
 case 'meta':{const health=await request('GET','/healthz');return {version:app.getVersion(),stage:health.stage,dataDir,platform:process.platform,health:health.status};}
 case 'choose.directory':{const selected=await dialog.showOpenDialog(window,{title:'选择要导入的项目目录',properties:['openDirectory']});return selected.canceled?null:selected.filePaths[0];}
 case 'choose.document':{const selected=await dialog.showOpenDialog(window,{title:'选择项目资料',properties:['openFile'],filters:[{name:'项目资料',extensions:['md','markdown','txt','pdf']}]});return selected.canceled?null:selected.filePaths[0];}
 case 'projects.list':return request('GET','/api/projects');
 case 'project.create':return request('POST','/api/projects',payload);
 case 'project.get':return request('GET','/api/projects/'+projectID(payload));
 case 'message.append':return request('POST',`/api/projects/${projectID(payload)}/messages`,{text:payload.text,idempotency_key:payload.idempotency_key});
 case 'plan.create':return request('POST',`/api/projects/${projectID(payload)}/plans`,{content:payload.content,reason:payload.reason,base_version:payload.base_version,idempotency_key:payload.idempotency_key});
 case 'plan.update':case 'plan.confirm':{if(!Number.isInteger(payload.version)||payload.version<1)throw new Error('方案版本无效');const body={expected_hash:payload.expected_hash,expected_revision:payload.expected_revision,idempotency_key:payload.idempotency_key};if(operation==='plan.update')body.content=payload.content;return request(operation==='plan.confirm'?'POST':'PUT',`/api/projects/${projectID(payload)}/plans/${payload.version}${operation==='plan.confirm'?'/confirm':''}`,body);}
 case 'document.add':return request('POST',`/api/projects/${projectID(payload)}/documents`,{path:payload.path,idempotency_key:payload.idempotency_key});
 case 'plan.template':return request('GET','/api/plan-template');
 case 'permission.demo':return request('POST',`/api/projects/${projectID(payload)}/permission-demo`,{});
 case 'events.start':return startEvents(projectID(payload),sender);
 case 'events.stop':stopEvents();return {};
 case 'connections.list':return syncConnections();
 case 'roles.list':await syncConnections();return request('GET','/api/roles');
 case 'roles.templates':return request('GET','/api/role-templates');
 case 'roles.save':{const id=projectID({id:payload.role?.id});await syncConnections();return request('PUT','/api/roles/'+id,payload.role);}
 case 'roles.delete':return request('DELETE',`/api/roles/${projectID(payload)}?revision=${Number(payload.revision)}`);
 case 'native.sessions.list':return request('GET',`/api/projects/${projectID(payload)}/native-sessions`);
 case 'native.sessions.get':return request('GET',`/api/projects/${projectID(payload)}/native-sessions/${projectID({id:payload.session_id})}`);
 case 'runs.list':return request('GET',`/api/projects/${projectID(payload)}/runs`);
 case 'runs.get':return request('GET',`/api/projects/${projectID(payload)}/runs/${projectID({id:payload.run_id})}`);
 case 'runs.cancel':return request('POST',`/api/projects/${projectID(payload)}/runs/${projectID({id:payload.run_id})}/cancel`,{});
 case 'runs.start':{
  await syncConnections();const roles=await request('GET','/api/roles');const role=roles.find(item=>item.id===payload.role_id);
  if(!role||role.revision!==payload.role_revision)throw new Error('角色已变化，请重新查看后试运行');
  const choices=[role.primary,...(role.fallback.enabled?role.fallback.candidates:[])];const resolved=await runValues(choices);
  return request('POST',`/api/projects/${projectID(payload)}/runs`,{role_id:role.id,role_revision:role.revision,input:payload.input,scope:payload.scope,idempotency_key:payload.idempotency_key,connection_revisions:resolved.revisions,values:resolved.values});
 }
 case 'runs.resume':{const id=projectID(payload),runID=projectID({id:payload.run_id});const run=await request('GET',`/api/projects/${id}/runs/${runID}`);const resolved=await runValues(run.choices);return request('POST',`/api/projects/${id}/runs/${runID}/resume`,{values:resolved.values});}
 case 'usage.get':return request('GET','/api/usage');
 case 'budget.save':{if(!['CNY','USD'].includes(payload.budget?.currency))throw new Error('预算币种无效');return request('PUT','/api/budgets/'+payload.budget.currency,payload.budget);}
 case 'accounts.list':return [await codexAccount.describe(),await claudeReadiness()];
 case 'accounts.check':case 'accounts.login':case 'accounts.cancel':case 'accounts.logout':{
  if(payload.provider!=='codex')throw new Error('Claude 强制出口链路尚未验证，当前不启动其登录或网络请求');
  if(operation==='accounts.check')return codexAccount.check();
  if(operation==='accounts.login')return codexAccount.login();
  if(operation==='accounts.cancel')return codexAccount.cancelLogin();
  return codexAccount.logout();
 }
 case 'connections.delete':{const id=projectID(payload);await request('DELETE',`/api/connections/${id}?revision=${Number(payload.revision)}`);return connections.remove(payload.id,payload.revision);}
 case 'connections.check':return request('POST','/api/connections/check',payload.config);
 case 'connections.save':case 'connections.test':{
  await request('POST','/api/connections/check',payload.config);const prepared=await connections.prepare(payload);
  await request('POST','/api/connections/validate',connections.projection(prepared.summary));
  if(prepared.entry.config.egress.mode==='explicit_proxy'&&!prepared.entry.secrets.proxy)throw new Error('请填写出口代理 URL');
  if(prepared.entry.config.egress.mode==='explicit_proxy')await request('POST','/api/proxies/validate',{url:prepared.entry.secrets.proxy});
  // Re-read revisions after asynchronous validation, before the synchronous write.
  if(operation==='connections.save'){if(payload.id)await request('POST',`/api/connections/${projectID(payload)}/revoke`,{});const saved=await connections.save(payload);await request('PUT','/api/connections/'+saved.id,connections.projection(saved));return saved;}
  return request('POST','/api/connections/test',{config:prepared.entry.config,connection:connections.projection(prepared.summary),values:connections.values(prepared.entry),idempotency_key:payload.idempotency_key});
 }
 default:throw new Error('操作不受支持');
 }
}

const single=app.requestSingleInstanceLock();if(!single)app.quit();else{
 app.on('second-instance',()=>{if(window){if(window.isMinimized())window.restore();window.focus();}});
 app.whenReady().then(async()=>{
  await startBackend();connections=new ConnectionStore(dataDir,safeStorage);
  codexAccount=new CodexAccount(dataDir,{openExternal:url=>shell.openExternal(url)});
  const icon=nativeImage.createFromPath(path.join(root,'assets','app.png'));if(app.dock)app.dock.setIcon(icon);
  window=new BrowserWindow({show:false,width:1320,height:900,minWidth:960,minHeight:680,title:'Agent Workbench',backgroundColor:nativeTheme.shouldUseDarkColors?'#0e1622':'#f3f6fa',icon,webPreferences:{preload:path.join(__dirname,'preload.cjs'),nodeIntegration:false,contextIsolation:true,sandbox:true,webSecurity:true,webviewTag:false}});
  window.once('ready-to-show',()=>{if(!smokeCheck)window.show()});
  window.webContents.session.setPermissionRequestHandler((_contents,_permission,callback)=>callback(false));window.webContents.setWindowOpenHandler(()=>({action:'deny'}));window.webContents.on('will-navigate',event=>event.preventDefault());window.webContents.on('will-attach-webview',event=>event.preventDefault());
  ipcMain.handle('workbench:invoke',async(event,operation,payload)=>{try{if(event.sender!==window.webContents||event.senderFrame?.url.split('#')[0]!==indexURL)throw new Error('此页面没有桌面操作权限');const value=await invoke(operation,payload||{},event.sender);startupOperations.add(operation);setImmediate(checkStartup);return {ok:true,value};}catch(error){return {ok:false,error:error.message||'操作失败'};}});
  if(smokeCheck)smokeTimer=setTimeout(()=>{process.stderr.write('桌面启动握手超时\n');app.quit();},15000);
  await window.loadFile(indexFile);
  loaded=true;checkStartup();
 }).catch(error=>{if(smokeCheck){process.stderr.write('桌面启动检查失败：'+error.message+'\n');process.exitCode=1;}else dialog.showErrorBox('工作台启动失败',error.message);app.quit();});
 process.once('SIGTERM',()=>app.quit());process.once('SIGINT',()=>app.quit());
 app.on('before-quit',event=>{
  if(quitting)return;
  event.preventDefault();quitting=true;clearTimeout(smokeTimer);stopEvents();
  const stoppingBackend=new Promise(resolve=>{
   if(!backend||backend.exitCode!==null||backend.signalCode!==null){resolve();return;}
   const timeout=setTimeout(()=>{backend.kill('SIGKILL');app.exit(1);},5000);
   backend.once('exit',()=>{clearTimeout(timeout);resolve();});backend.kill('SIGTERM');
  });
  Promise.all([stoppingBackend,codexAccount?.close()]).then(()=>app.quit()).catch(()=>app.exit(1));
 });app.on('window-all-closed',()=>app.quit());
}

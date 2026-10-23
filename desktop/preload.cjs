const {contextBridge,ipcRenderer}=require('electron');
const operations=new Set(['appearance.set','native.sessions.list','native.sessions.get','meta','projects.list','project.get','project.create','message.append','plan.create','plan.update','plan.confirm','document.add','plan.template','permission.demo','events.start','events.stop','choose.directory','choose.document','connections.list','connections.check','connections.save','connections.test','connections.delete','roles.list','roles.templates','roles.save','roles.delete','runs.list','runs.get','runs.start','runs.cancel','runs.resume','usage.get','budget.save','accounts.list','accounts.check','accounts.login','accounts.cancel','accounts.logout']);
contextBridge.exposeInMainWorld('workbench',{
  invoke:(operation,payload={})=>{if(!operations.has(operation))return Promise.reject(new Error('操作不受支持'));return ipcRenderer.invoke('workbench:invoke',operation,payload);},
  onEvent:listener=>{const callback=(_event,value)=>listener(value);ipcRenderer.on('workbench:event',callback);return()=>ipcRenderer.removeListener('workbench:event',callback);}
});

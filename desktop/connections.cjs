const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');

// Only the Electron main process can decrypt this local configuration file.
class ConnectionStore {
  constructor(directory, encryption) { this.directory=directory; this.file=path.join(directory,'connections.enc'); this.encryption=encryption; }
  read() {
    if (!fs.existsSync(this.file)) return {version:1, pepper:crypto.randomBytes(32).toString('hex'), entries:[]};
    try { const state=JSON.parse(this.encryption.decryptString(fs.readFileSync(this.file))); if(state.version!==1 || !Array.isArray(state.entries) || !state.pepper)throw new Error(); return state; }
    catch { throw new Error('无法解密已有连接配置。请使用原来的 macOS 账号与应用打开，配置文件已保留。'); }
  }
  write(state) {
    if (!this.encryption.isEncryptionAvailable()) throw new Error('系统凭证加密尚未就绪，连接没有保存。');
    const encrypted=this.encryption.encryptString(JSON.stringify(state));
    fs.mkdirSync(this.directory,{recursive:true,mode:0o700}); const tmp=this.file+'.'+crypto.randomUUID()+'.tmp';
    try {fs.writeFileSync(tmp,encrypted,{mode:0o600,flag:'wx'});fs.renameSync(tmp,this.file);}
    finally {if(fs.existsSync(tmp))fs.unlinkSync(tmp);}
  }
  summary(entry) {
    return {id:entry.id, label:entry.label, revision:entry.revision, config:entry.config, hasKey:!!entry.secrets.key,
      hasProxy:!!entry.secrets.proxy, configuredHeaders:Object.keys(entry.secrets.headers||{}), updatedAt:entry.updatedAt};
  }
  list() {return this.read().entries.map(entry=>this.summary(entry));}
  prepare(input,state=this.read()) {
    if (!input || typeof input!=='object' || typeof input.label!=='string' || !input.label.trim() || input.label.length>128)throw new Error('请填写连接名称');
    if(Buffer.byteLength(JSON.stringify(input))>65536)throw new Error('连接配置过大');
    const old=state.entries.find(entry=>entry.id===input.id);
    if(input.id && !old)throw new Error('连接不存在，请刷新列表');
    if(old && old.revision!==input.revision)throw new Error('连接已被修改，请刷新后重试');
    const config=structuredClone(input.config); if(!config || typeof config!=='object')throw new Error('配置无效');
    const secrets={key:input.clearKey?'':(input.key||old?.secrets.key||''),proxy:input.clearProxy?'':(input.proxy||old?.secrets.proxy||''),headers:{}};
    if(config.auth_kind==='none')secrets.key='';
    if(config.egress?.mode==='system') {secrets.proxy='';delete config.proxy_env;}
    for(const [name,ref]of Object.entries(config.header_envs||{})) {
      if(typeof ref!=='string')throw new Error('请求头引用无效');
      const value=input.headers?.[name]||old?.secrets.headers?.[name]||'';
      if(value)secrets.headers[name]=value;
    }
    for(const value of [secrets.key,secrets.proxy,...Object.values(secrets.headers)])if(typeof value!=='string' || value.length>8192 || /[\r\n\0]/.test(value))throw new Error('密钥、代理或请求头中包含无效字符');
    const entry={id:old?.id||crypto.randomUUID(),label:input.label.trim(),revision:(old?.revision||0)+1,config,secrets,updatedAt:new Date().toISOString()};
    const origin=new URL(config.endpoint.address).origin;
    const fingerprint=value=>crypto.createHmac('sha256',Buffer.from(state.pepper,'hex')).update(value).digest('hex');
    if(secrets.key) {
      entry.account=fingerprint(origin+'\0'+secrets.key);entry.route=fingerprint(config.egress.mode+'\0'+secrets.proxy);
      if(state.entries.some(other=>other.id!==entry.id && other.account===entry.account && other.route!==entry.route))throw new Error('同一 API 账号已有不同网络绑定，请先统一该账号的连接设置');
    }
    return {entry,state};
  }
  savePrepared({entry,state}) {
    state.entries=state.entries.filter(item=>item.id!==entry.id).concat(entry);this.write(state);return this.summary(entry);
  }
  values(entry) {
    const values={};if(entry.config.key_env)values[entry.config.key_env]=entry.secrets.key;
    if(entry.config.proxy_env)values[entry.config.proxy_env]=entry.secrets.proxy;
    for(const [name,ref]of Object.entries(entry.config.header_envs||{}))values[ref]=entry.secrets.headers[name]||'';
    return values;
  }
}
module.exports={ConnectionStore};

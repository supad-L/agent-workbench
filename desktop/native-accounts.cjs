const fs = require('node:fs/promises');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { StringDecoder } = require('node:string_decoder');
const { randomUUID } = require('node:crypto');

async function findExecutable(name, candidates = []) {
  for (const candidate of [...candidates, ...(process.env.PATH || '').split(path.delimiter).filter(Boolean).map(folder => path.join(folder, process.platform === 'win32' ? name + '.exe' : name))]) {
    try { if ((await fs.stat(candidate)).isFile()) return candidate; } catch { /* A missing candidate is not an authentication failure. */ }
  }
  return null;
}
async function discoverCodex() {
  return findExecutable('codex', ['/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex', '/Applications/Codex.app/Contents/Resources/codex']);
}
function authURL(value) {
  let url;
  try { url = new URL(value); } catch { throw new Error('官方登录地址无效，未打开浏览器'); }
  if (url.protocol !== 'https:' || !['auth.openai.com', 'chatgpt.com', 'chat.openai.com'].includes(url.hostname) || url.username || url.password || url.port && url.port !== '443') throw new Error('登录地址不属于允许的官方站点，未打开浏览器');
  const redirect = url.searchParams.get('redirect_uri');
  if (redirect) {
    let callback;
    try { callback = new URL(redirect); } catch { throw new Error('登录回调地址无效'); }
    if (callback.protocol !== 'http:' || !['localhost', '127.0.0.1', '[::1]'].includes(callback.hostname) || callback.username || callback.password) throw new Error('登录回调必须由本地官方执行器接收');
  }
  return url.href;
}

// This client exposes only official account RPCs. It never starts a thread or executes code.
class CodexAccount {
  constructor(directory, { spawnProcess = spawn, discover = discoverCodex, openExternal, timeout = 10000, coordination } = {}) {
    this.directory = directory; this.home = path.join(directory, 'native-accounts', 'codex');
    this.spawnProcess = spawnProcess; this.discover = discover; this.openExternal = openExternal;
    this.timeout = timeout; this.pending = new Map(); this.sequence = 0; this.queue = Promise.resolve();
    this.child = null; this.ready = null; this.closing = false; this.loginId = null; this.loginTimer = null;
    this.completedLogins = new Set(); this.account = { checked: false, loggedIn: false, authType: 'unknown', planType: null };
    this.coordination = coordination; this.coordinationState = null;
    this.ownerFile = path.join(this.home, '.workbench-auth-owner'); this.ownerReceipt = null;
    this.stopping = new WeakMap();
  }
  exclusive(operation) { const result = this.queue.then(operation); this.queue = result.catch(() => {}); return result; }
  managed(kind, operation) {
    return this.exclusive(async () => {
      if (this.closing) throw new Error('账号服务正在关闭');
      // Reserve the backend owner before touching the CLI or its authentication.
      const receipt = this.coordination ? await this.coordination.begin(kind) : null;
      try {
        if (this.closing) throw new Error('账号服务正在关闭');
        await operation();
        // Pending browser authorization still owns the chain. Otherwise stop the
        // official auth process before granting any native execution ownership.
        if (this.coordination && !this.loginId) await this.stopChild(this.child);
        if (receipt) this.coordinationState = await this.coordination.finish({ operation: receipt.operation, authenticated: this.account.loggedIn, owner_active: !!this.loginId, healthy: true });
        return this.describe();
      } catch (error) {
        if (receipt) {
          let healthy = true;
          try { await this.stopChild(this.child); } catch { healthy = false; }
          this.account = { checked: false, loggedIn: false, authType: 'unknown', planType: null };
          try { this.coordinationState = await this.coordination.finish({ operation: receipt.operation, authenticated: false, owner_active: !healthy, healthy }); }
          catch { throw new Error('账号协调结果未确认，当前不可交接执行，请重启后核对'); }
        }
        throw error;
      }
    });
  }
  async describe() {
    const executable = await this.discover();
    return { provider: 'codex', name: 'GPT / ChatGPT', installed: !!executable, executable,
      ...this.account, loginPending: !!this.loginId, loginReady: !!executable, executionReady: false,
      coordination: this.coordinationState,
      network: 'system', reason: '原生任务运行仍需通过 CLI 与隔离环境组合验收。' };
  }
  async start() {
    if (this.closing) throw new Error('账号服务正在关闭');
    if (this.ready) return this.ready;
    if (this.child && (this.child.exitCode !== null || this.child.signalCode != null)) await this.stopChild(this.child);
    this.ready = (async () => {
      const binary = await this.discover(); if (!binary) throw new Error('未找到官方 Codex CLI，请先安装或更新官方应用');
      await fs.mkdir(this.home, { recursive: true, mode: 0o700 });
      const base = await fs.realpath(this.directory), actual = await fs.realpath(this.home);
      if (actual !== path.join(base, 'native-accounts', 'codex')) throw new Error('工作台认证目录不能指向其他账号目录的符号链接');
      if(this.closing)throw new Error('账号服务正在关闭');
      const receipt = randomUUID();
      try { await fs.writeFile(this.ownerFile, receipt, { flag: 'wx', mode: 0o600, flush: true }); }
      catch { throw new Error('认证进程的独占状态未确认，请先核对上次授权进程是否已停止'); }
      this.ownerReceipt = receipt;
      if(this.closing)throw new Error('账号服务正在关闭');
      const env = {};
      for (const name of ['PATH', 'HOME', 'USER', 'LOGNAME', 'TMPDIR', 'TEMP', 'TMP', 'LANG', 'LC_ALL', 'SYSTEMROOT', 'WINDIR']) if (process.env[name]) env[name] = process.env[name];
      env.CODEX_HOME = this.home;
      const child = this.spawnProcess(binary, ['app-server', '--stdio', '-c', 'cli_auth_credentials_store="keyring"', '-c', 'analytics.enabled=false'], { cwd: this.home, env, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true });
      this.child = child;
      child.stderr.resume(); // Never log raw native authentication diagnostics.
      const decoder = new StringDecoder('utf8'); let buffer = '';
      child.stdout.on('data', chunk => {
        buffer += decoder.write(chunk);
        if (buffer.length > 1048576) { this.fail(new Error('官方账号接口响应超过上限')); child.kill('SIGTERM'); return; }
        let newline;
        while ((newline = buffer.indexOf('\n')) !== -1) {
          const line = buffer.slice(0, newline); buffer = buffer.slice(newline + 1);
          if (!line.trim()) continue;
          try { this.receive(JSON.parse(line)); } catch { this.fail(new Error('官方账号接口返回了无效数据')); child.kill('SIGTERM'); }
        }
      });
      child.on('error', () => { this.fail(new Error('无法启动官方账号接口')); this.ready = null; });
      child.on('exit', () => { if(this.child!==child)return;this.fail(new Error('官方账号接口已退出')); this.ready = null; clearTimeout(this.loginTimer); this.loginId = null; });
      await this.rpc('initialize', { clientInfo: { name: 'agent_workbench', title: 'Agent Workbench', version: '0.1.0' } });
      this.send({ method: 'initialized', params: {} });
    })();
    try { await this.ready; } catch (error) {const child=this.child;try{if(child)await this.stopChild(child);else await this.releaseOwner()}catch(stopError){this.closing=true;throw stopError}this.ready = null; throw error; }
  }
  send(value) { if (!this.child || this.child.killed) throw new Error('官方账号接口未连接'); this.child.stdin.write(JSON.stringify(value) + '\n', error => { if (error) this.fail(new Error('官方账号接口连接中断')); }); }
  rpc(method, params = {}) {
    if (!['initialize', 'account/read', 'account/login/start', 'account/login/cancel', 'account/logout'].includes(method)) return Promise.reject(new Error('账号接口不允许此操作'));
    return new Promise((resolve, reject) => {
      const id = ++this.sequence, timer = setTimeout(() => { this.pending.delete(id); reject(new Error('官方账号接口超时，请检查 CLI 与本机网络')); }, this.timeout);
      this.pending.set(id, { resolve, reject, timer });
      try { this.send({ id, method, params }); } catch (error) { clearTimeout(timer); this.pending.delete(id); reject(error); }
    });
  }
  receive(value) {
    if (value.method && value.id !== undefined) { this.send({ id: value.id, error: { code: -32601, message: 'Only account operations are supported' } }); return; }
    if (value.id !== undefined) {
      const pending = this.pending.get(value.id); if (!pending) return;
      clearTimeout(pending.timer); this.pending.delete(value.id);
      if (value.error) pending.reject(new Error('官方账号操作失败，请检查 CLI 版本、系统凭证存储及网络'));
      else pending.resolve(value.result);
      return;
    }
    if (value.method === 'account/login/completed') {
      const id = value.params?.loginId;
      if (id) { this.completedLogins.add(id); if (this.completedLogins.size > 32) this.completedLogins.delete(this.completedLogins.values().next().value); }
      if (id === this.loginId) { this.loginId = null; clearTimeout(this.loginTimer); }
    }
  }
  fail(error) { for (const pending of this.pending.values()) { clearTimeout(pending.timer); pending.reject(error); } this.pending.clear(); }
  async readAccount(refreshToken = false) {
    const result = await this.rpc('account/read', { refreshToken });
    this.account = { checked: true, loggedIn: result?.account?.type === 'chatgpt', authType: result?.account?.type || 'none', planType: typeof result?.account?.planType === 'string' ? result.account.planType : null };
    return this.describe();
  }
  check() { return this.managed('check', async () => { await this.start(); await this.readAccount(); }); }
  // Internal only: no polling refresh, token export, or renderer refresh RPC.
  refresh() { return this.managed('refresh', async () => { await this.start(); await this.readAccount(true); }); }
  login() {
    return this.managed('login', async () => {
      await this.start(); if (this.loginId) return this.describe();
      const result = await this.rpc('account/login/start', { type: 'chatgpt' });
      if (!result?.loginId || typeof result.loginId !== 'string') throw new Error('官方登录流程未返回有效编号');
      let url;
      try { url = authURL(result.authUrl); } catch (error) { await this.rpc('account/login/cancel', { loginId: result.loginId }); throw error; }
      if (!this.completedLogins.has(result.loginId)) {
        this.loginId = result.loginId;
        this.loginTimer = setTimeout(() => { this.cancelLogin().catch(() => {}); }, 10 * 60 * 1000);
        try { await this.openExternal(url); } catch { await this.rpc('account/login/cancel', { loginId: result.loginId }); this.loginId = null; clearTimeout(this.loginTimer); throw new Error('无法打开系统浏览器，登录已取消'); }
      }else return this.readAccount();
      return this.describe();
    });
  }
  cancelLogin() { return this.managed('cancel', async () => { await this.start(); if (this.loginId) { await this.rpc('account/login/cancel', { loginId: this.loginId }); this.loginId = null; clearTimeout(this.loginTimer); } await this.readAccount(); }); }
  logout() { return this.managed('logout', async () => { await this.start(); await this.rpc('account/logout'); this.loginId = null; clearTimeout(this.loginTimer); await this.readAccount(); }); }
  async releaseOwner() {
    if (!this.ownerReceipt) return;
    // Only an owner created by this instance may be cleared, after confirmed exit
    // (or a synchronous spawn failure). Never remove another process's marker.
    const value = await fs.readFile(this.ownerFile, 'utf8');
    if (value !== this.ownerReceipt) throw new Error('认证进程所有权已变化，未清除占用');
    await fs.unlink(this.ownerFile); this.ownerReceipt = null;
  }
  stopChild(child) {
    if (!child) return Promise.resolve();
    if (!this.stopping.has(child)) this.stopping.set(child, this.stopChildOnce(child));
    return this.stopping.get(child);
  }
  async stopChildOnce(child){
    if(child.exitCode==null&&child.signalCode==null)await new Promise((resolve,reject)=>{
      let finished=false;
      const complete=()=>{if(finished)return;finished=true;clearTimeout(killTimer);clearTimeout(deadline);child.removeListener('exit',complete);child.removeListener('close',complete);resolve()};
      const killTimer=setTimeout(()=>child.kill('SIGKILL'),2000);
      const deadline=setTimeout(()=>{if(finished)return;finished=true;child.removeListener('exit',complete);child.removeListener('close',complete);reject(new Error('无法停止官方账号进程，账号服务保持关闭'))},3000);
      child.once('exit',complete);child.once('close',complete);child.kill('SIGTERM');
    });
    await this.releaseOwner();
    if(this.child===child){this.child=null;this.ready=null;}
  }
  async close() {
    this.closing = true; clearTimeout(this.loginTimer); this.fail(new Error('账号服务已关闭'));
    await this.stopChild(this.child);
  }
}
async function claudeReadiness() {
  return { provider: 'claude', name: 'Claude 订阅', installed: !!await findExecutable('claude'), checked: false, loggedIn: false, loginReady: false, executionReady: false,
    reason: '登录浏览器、续期与推理的强制住宅出口尚未验证；当前不启动 Claude 登录或 CLI 网络请求。' };
}
module.exports = { CodexAccount, claudeReadiness, authURL };

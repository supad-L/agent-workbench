const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { EventEmitter } = require('node:events');
const { PassThrough, Writable } = require('node:stream');
const { CodexAccount, authURL } = require('./native-accounts.cjs');

function fixture(t, badURL = false, coordination) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'awb-native-auth-'));
  const opened = [], methods = [], events = [], reads = []; let child, authenticated = false;
  const account = new CodexAccount(directory, { coordination, discover: async () => '/synthetic/codex', openExternal: async url => opened.push(url), timeout: 100,
    spawnProcess: (_binary, args, options) => {
      assert.equal(options.env.CODEX_HOME, path.join(directory, 'native-accounts', 'codex'));
      assert.equal(options.env.OPENAI_API_KEY, undefined);
      assert.equal(options.env.CODEX_API_KEY, undefined);
      assert.ok(args.includes('cli_auth_credentials_store="keyring"'));
      events.push('spawn');
      child = new EventEmitter(); child.stdout = new PassThrough(); child.stderr = new PassThrough(); child.exitCode = null; child.killed = false;
      child.kill = () => { child.killed = true; child.exitCode = 0; queueMicrotask(() => { events.push('exit'); child.emit('exit', 0); }); };
      child.stdin = new Writable({ write(bytes, _encoding, done) {
        const value = JSON.parse(bytes.toString()); methods.push(value.method);
        if (value.id !== undefined) {
          let result = {};
          if (value.method === 'account/read') { reads.push(value.params); result = { account: authenticated ? { type: 'chatgpt', planType: 'plus', accessToken: 'SYNTHETIC_MUST_NOT_ESCAPE' } : null }; }
          if (value.method === 'account/login/start') result = { loginId: 'login-fixture', authUrl: badURL ? 'https://auth.openai.com.evil.invalid/login' : 'https://auth.openai.com/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback' };
          if (value.method === 'account/logout') authenticated = false;
          queueMicrotask(() => child.stdout.write(JSON.stringify({ id: value.id, result }) + '\n'));
        }
        done();
      } });
      return child;
    } });
  t.after(async () => { await account.close(); fs.rmSync(directory, { recursive: true }); });
  return { account, opened, methods, events, reads, complete() { authenticated = true; child.stdout.write(JSON.stringify({ method: 'account/login/completed', params: { loginId: 'login-fixture', success: true } }) + '\n'); } };
}
test('官方账号专用目录、单一授权所有者、脱敏状态与退出', async t => {
  const { account, opened, methods, complete } = fixture(t);
  const initial = await account.check(); assert.equal(initial.loggedIn, false);
  await Promise.all([account.login(), account.login()]);
  assert.equal(methods.filter(method => method === 'account/login/start').length, 1);
  assert.equal(opened.length, 1);
  complete(); const loggedIn = await account.check();
  assert.equal(loggedIn.loggedIn, true); assert.equal(loggedIn.executionReady, false);
  assert.equal(JSON.stringify(loggedIn).includes('SYNTHETIC_MUST_NOT_ESCAPE'), false);
  assert.equal((await account.logout()).loggedIn, false);
  await assert.rejects(account.rpc('thread/start'), /不允许/);
});
test('拒绝非官方登录地址，不打开浏览器并取消原生流程', async t => {
  const { account, opened, methods } = fixture(t, true);
  await assert.rejects(account.login(), /不属于/);
  assert.equal(opened.length, 0); assert.ok(methods.includes('account/login/cancel'));
  assert.throws(() => authURL('https://auth.openai.com/authorize?redirect_uri=https://untrusted.invalid'), /本地/);
  assert.throws(() => authURL('file:///tmp/login'), /不属于/);
});
test('未安装 CLI 时保持未就绪，不启动或读取其他账号', async t => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'awb-missing-auth-'));
  t.after(() => fs.rmSync(directory, { recursive: true }));
  const account = new CodexAccount(directory, { discover: async () => null });
  assert.equal((await account.describe()).installed, false);
  await assert.rejects(account.check(), /未找到/);
  await account.close();
});

test('后端认证许可覆盖登录、刷新和退出，交接前确认官方进程退出', async t => {
  let active = false, number = 0, resource = { state: 'unverified' };
  const receipts = [], order = [];
  const coordination = {
    async begin(kind) { assert.equal(active, false); active = true; order.push(kind); return { operation: String(++number) }; },
    async finish(result) {
      assert.equal(active, true);
      if (!result.owner_active) assert.equal(events.at(-1), 'exit', 'published before auth owner exit');
      receipts.push(result); active = false;
      resource = { state: result.owner_active ? 'authenticating' : result.authenticated ? 'idle' : 'unverified' };
      return resource;
    },
  };
  const { account, events, reads, complete } = fixture(t, false, coordination);
  assert.equal((await account.login()).coordination.state, 'authenticating');
  assert.equal(receipts[0].owner_active, true);
  complete();
  assert.equal((await account.check()).coordination.state, 'idle');
  await Promise.all([account.refresh(), account.refresh()]);
  assert.deepEqual(reads.map(value => value.refreshToken), [false, true, true]);
  assert.equal(events.filter(value => value === 'spawn').length, 3);
  assert.equal(events.filter(value => value === 'exit').length, 3);
  const state = await account.logout();
  assert.equal(state.loggedIn, false); assert.equal(state.executionReady, false);
  assert.equal(JSON.stringify(state).includes('SYNTHETIC_MUST_NOT_ESCAPE'), false);
  assert.deepEqual(order, ['login', 'check', 'refresh', 'refresh', 'logout']);
});

test('后端拒绝占用时不接触官方进程，认证失败先停止再发布未授权', async t => {
  let allow = false; const receipts = [];
  const { account, events, opened } = fixture(t, true, {
    begin: async () => { if (!allow) throw new Error('账号正在执行'); return { operation: 'fixture' }; },
    finish: async result => { assert.equal(events.at(-1), 'exit'); receipts.push(result); return { state: 'unverified' }; },
  });
  await assert.rejects(account.check(), /正在执行/);
  assert.deepEqual(events, []);
  allow = true;
  await assert.rejects(account.login(), /不属于/);
  assert.equal(opened.length, 0);
  assert.deepEqual(receipts, [{ operation:'fixture', authenticated:false, owner_active:false, healthy:true }]);
});

test('同一认证目录的独占标记阻止其他实例，确认旧进程退出后才允许接手', async t => {
  const { account } = fixture(t);
  await account.check();
  assert.equal(fs.statSync(account.ownerFile).mode & 0o777, 0o600);
  let spawns = 0;
  const contender = new CodexAccount(account.directory, {
    discover: account.discover,
    spawnProcess: (...args) => { spawns++; return account.spawnProcess(...args); },
  });
  t.after(() => contender.close());
  await assert.rejects(contender.check(), /独占状态未确认/);
  assert.equal(spawns, 0);
  assert.equal(fs.existsSync(account.ownerFile), true);
  await account.close();
  await contender.check();
  assert.equal(spawns, 1);
  await contender.close();
  assert.equal(fs.existsSync(account.ownerFile), false);
});

test('取消请求不会提前删除认证占用，并发收尾只清除一次', async t => {
  const { account } = fixture(t);
  await account.check();
  const child = account.child;
  child.kill = () => true; // SIGTERM delivered, but process exit is not yet confirmed.
  const first = account.stopChild(child), second = account.stopChild(child);
  assert.equal(fs.existsSync(account.ownerFile), true);
  child.exitCode = 0; child.emit('exit', 0);
  await Promise.all([first, second]);
  assert.equal(fs.existsSync(account.ownerFile), false);
  assert.equal(account.child, null);
});

test('收到信号退出的旧认证进程已停止，重新检测不会等待第二次退出', async t => {
  const { account, events } = fixture(t);
  await account.check();
  account.child.signalCode = 'SIGKILL';
  account.child.emit('exit', null, 'SIGKILL');
  await account.check();
  assert.equal(events.filter(value => value === 'spawn').length, 2);
});

test('认证进程停止未确认时保留独占标记并报告交接阻塞', async t => {
  const receipts = [];
  const { account, complete } = fixture(t, false, {
    begin: async () => ({ operation:'fixture' }),
    finish: async result => { receipts.push(result); return { state: result.healthy ? 'authenticating' : 'blocked' }; },
  });
  await account.login();
  complete();
  const stop = account.stopChild;
  account.stopChild = async () => { throw new Error('stop unconfirmed'); };
  try { await assert.rejects(account.check(), /stop unconfirmed/); }
  finally { account.stopChild = stop; }
  assert.equal(fs.existsSync(account.ownerFile), true);
  assert.equal(receipts.at(-1).healthy, false);
  assert.equal(receipts.at(-1).owner_active, true);
  assert.equal((await account.describe()).coordination.state, 'blocked');
});

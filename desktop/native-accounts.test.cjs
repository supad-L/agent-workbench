const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { EventEmitter } = require('node:events');
const { PassThrough, Writable } = require('node:stream');
const { CodexAccount, authURL } = require('./native-accounts.cjs');

function fixture(t, badURL = false) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'awb-native-auth-'));
  const opened = [], methods = []; let child, authenticated = false;
  const account = new CodexAccount(directory, { discover: async () => '/synthetic/codex', openExternal: async url => opened.push(url), timeout: 100,
    spawnProcess: (_binary, args, options) => {
      assert.equal(options.env.CODEX_HOME, path.join(directory, 'native-accounts', 'codex'));
      assert.equal(options.env.OPENAI_API_KEY, undefined);
      assert.equal(options.env.CODEX_API_KEY, undefined);
      assert.ok(args.includes('cli_auth_credentials_store="keyring"'));
      child = new EventEmitter(); child.stdout = new PassThrough(); child.stderr = new PassThrough(); child.exitCode = null; child.killed = false;
      child.kill = () => { child.killed = true; child.exitCode = 0; queueMicrotask(() => child.emit('exit', 0)); };
      child.stdin = new Writable({ write(bytes, _encoding, done) {
        const value = JSON.parse(bytes.toString()); methods.push(value.method);
        if (value.id !== undefined) {
          let result = {};
          if (value.method === 'account/read') result = { account: authenticated ? { type: 'chatgpt', planType: 'plus', accessToken: 'SYNTHETIC_MUST_NOT_ESCAPE' } : null };
          if (value.method === 'account/login/start') result = { loginId: 'login-fixture', authUrl: badURL ? 'https://auth.openai.com.evil.invalid/login' : 'https://auth.openai.com/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback' };
          if (value.method === 'account/logout') authenticated = false;
          queueMicrotask(() => child.stdout.write(JSON.stringify({ id: value.id, result }) + '\n'));
        }
        done();
      } });
      return child;
    } });
  t.after(async () => { await account.close(); fs.rmSync(directory, { recursive: true }); });
  return { account, opened, methods, complete() { authenticated = true; child.stdout.write(JSON.stringify({ method: 'account/login/completed', params: { loginId: 'login-fixture', success: true } }) + '\n'); } };
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

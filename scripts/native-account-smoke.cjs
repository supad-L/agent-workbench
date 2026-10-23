// Real backend + official CLI, using a fresh empty namespace. No login or model.
const fs = require('node:fs/promises');
const path = require('node:path');
const { spawn } = require('node:child_process');
const readline = require('node:readline');
const { CodexAccount } = require('../desktop/native-accounts.cjs');

async function stopBackend(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => { child.kill('SIGKILL'); reject(new Error('backend shutdown unconfirmed')); }, 8000);
    child.once('exit', code => { clearTimeout(timer); code === 0 ? resolve() : reject(new Error('backend shutdown failed')); });
    child.kill('SIGTERM');
  });
}

async function main() {
  const root = path.resolve(__dirname, '..');
  await fs.mkdir(path.join(root, 'output'), { recursive: true });
  const directory = await fs.mkdtemp(path.join(root, 'output', 'native-account-smoke-'));
  let backend, account;
  try {
    backend = spawn(path.join(root, 'bin', 'agent-workbench'), ['serve', '--data-dir', path.join(directory, 'state')], { stdio: ['ignore', 'pipe', 'pipe'] });
    backend.stderr.resume();
    const ready = await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('backend startup timeout')), 15000);
      const reader = readline.createInterface({ input: backend.stdout });
      backend.once('error', () => { clearTimeout(timer); reject(new Error('backend startup failed')); });
      backend.once('exit', () => { clearTimeout(timer); reject(new Error('backend exited before handshake')); });
      reader.once('line', line => {
        clearTimeout(timer);
        try { const value = JSON.parse(line); if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(value.url) || typeof value.human_token !== 'string') throw new Error(); resolve(value); }
        catch { reject(new Error('invalid backend handshake')); }
      });
    });
    async function request(method, route, body) {
      const response = await fetch(ready.url + route, { method, headers: { Authorization: 'Bearer ' + ready.human_token, 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(20000) });
      if (!response.ok) throw new Error('account coordination failed');
      return response.json();
    }
    const initial = await request('GET', '/api/native-account');
    account = new CodexAccount(directory, {
      openExternal: async () => { throw new Error('login prohibited by probe'); },
      coordination: {
        begin: kind => request('POST', '/api/native-account/auth/begin', { kind }),
        finish: result => request('POST', '/api/native-account/auth/finish', result),
      },
    });
    const state = await account.check();
    if (!state.installed || !state.checked || state.loggedIn || state.authType !== 'none' || account.child !== null) throw new Error('empty account or owner exit not confirmed');
    const after = await request('GET', '/api/native-account');
    if (after.state !== 'unverified' || after.active !== 0 || after.limit !== 1 || initial.generation !== after.generation) throw new Error('unexpected slot state');
    let credentialFile = false;
    try { await fs.stat(path.join(account.home, 'auth.json')); credentialFile = true; } catch (error) { if (error.code !== 'ENOENT') throw error; }
    if (credentialFile) throw new Error('unexpected credential file; contents not read');
    const report = { status: 'passed', evidence: 'real_backend_official_cli_empty_namespace_no_login_no_inference', desktop_account_control: true, official_cli_exit_confirmed: true, unverified_slot: true, slot_limit: after.limit, plaintext_credential_file: false, execution_ready: state.executionReady };
    await fs.mkdir(path.join(root, 'output/verification'), { recursive: true });
    await fs.writeFile(path.join(root, 'output/verification/native-account-coordination.json'), JSON.stringify(report, null, 2) + '\n');
    process.stdout.write(JSON.stringify(report, null, 2) + '\n');
  } finally {
    try { await account?.close(); } finally { await stopBackend(backend); await fs.rm(directory, { recursive: true, force: true }); }
  }
}

main().catch(() => { process.stderr.write('原生账号协调探针未通过；未发起登录或模型请求。\n'); process.exitCode = 1; });

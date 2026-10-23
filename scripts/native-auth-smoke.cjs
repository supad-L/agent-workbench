// Inspect only a fresh workbench-owned Codex namespace; never opens login or starts a model.
const fs = require('node:fs/promises');
const path = require('node:path');
const { CodexAccount } = require('../desktop/native-accounts.cjs');
async function main() {
  const root = path.resolve(__dirname, '..'), output = path.join(root, 'output');
  await fs.mkdir(output, { recursive: true });
  const directory = await fs.mkdtemp(path.join(output, 'native-auth-smoke-'));
  const account = new CodexAccount(directory, { openExternal: async () => { throw new Error('本探针不允许发起登录'); } });
  try {
    const state = await account.check();
    if (!state.installed || !state.checked || state.loggedIn || state.authType !== 'none') throw new Error('独立认证目录未呈现预期的空账号状态，停止检查');
    let credentialFile = false;
    try { await fs.stat(path.join(account.home, 'auth.json')); credentialFile = true; } catch (error) { if (error.code !== 'ENOENT') throw error; }
    if (credentialFile) throw new Error('检测到意外的认证文件，本探针不读取其内容');
    const report = { evidence: 'official_cli_empty_workbench_namespace_no_login_no_inference', status: 'passed', initialize_and_account_read: true, isolated_empty_account: true, plaintext_credential_file: false };
    await fs.mkdir(path.join(output, 'verification'), { recursive: true });
    await fs.writeFile(path.join(output, 'verification', 'native-auth.json'), JSON.stringify(report, null, 2) + '\n');
    process.stdout.write(JSON.stringify(report, null, 2) + '\n');
  } finally { await account.close(); await fs.rm(directory, { recursive: true, force: true }); }
}
main().catch(() => { process.stderr.write('原生账号握手检查未通过；未登录或请求模型。\n'); process.exitCode = 1; });

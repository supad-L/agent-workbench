const fs = require('node:fs/promises');
const path = require('node:path');
const crypto = require('node:crypto');

// A single queue owns credential updates, including OS key rotation rewrites.
class ConnectionStore {
  constructor(directory, encryption) {
    this.directory = directory;
    this.file = path.join(directory, 'connections.enc');
    this.encryption = encryption;
    this.pending = Promise.resolve();
  }
  exclusive(operation) {
    const result = this.pending.then(operation);
    this.pending = result.catch(() => {});
    return result;
  }
  async read() {
    let encrypted;
    try { encrypted = await fs.readFile(this.file); }
    catch (error) {
      if (error.code === 'ENOENT') return { version: 1, pepper: crypto.randomBytes(32).toString('hex'), entries: [] };
      throw new Error('无法读取连接配置，原文件已保留。');
    }
    let state, rotated;
    try {
      const decrypted = await this.encryption.decryptStringAsync(encrypted);
      state = JSON.parse(decrypted.result);
      rotated = decrypted.shouldReEncrypt;
      if (state.version !== 1 || !Array.isArray(state.entries) || !/^[0-9a-f]{64}$/.test(state.pepper)) throw new Error();
    } catch { throw new Error('无法解密已有连接配置。请使用原来的系统账号与应用打开，配置文件已保留。'); }
    if (rotated) await this.write(state);
    return state;
  }
  async write(state) {
    if (!await this.encryption.isAsyncEncryptionAvailable() || this.encryption.getSelectedStorageBackend?.() === 'basic_text') throw new Error('系统凭证加密尚未就绪，连接没有保存。');
    const encrypted = await this.encryption.encryptStringAsync(JSON.stringify(state));
    await fs.mkdir(this.directory, { recursive: true, mode: 0o700 });
    const temporary = this.file + '.' + crypto.randomUUID() + '.tmp';
    try {
      await fs.writeFile(temporary, encrypted, { mode: 0o600, flag: 'wx' });
      await fs.rename(temporary, this.file);
    } finally { await fs.unlink(temporary).catch(error => { if (error.code !== 'ENOENT') throw error; }); }
  }
  models(entry) { return entry.models || [{ id: entry.config.model, tools: 'unknown', price_known: false }]; }
  account(state, entry) {
    const origin = new URL(entry.config.endpoint.address).origin;
    const identity = entry.secrets.key || JSON.stringify(Object.entries(entry.secrets.headers || {}).sort());
    return crypto.createHmac('sha256', Buffer.from(state.pepper, 'hex')).update(origin + '\0' + identity).digest('hex');
  }
  summary(entry, state) {
    return { id: entry.id, label: entry.label, revision: entry.revision, config: entry.config,
      hasKey: !!entry.secrets.key, hasProxy: !!entry.secrets.proxy,
      configuredHeaders: Object.keys(entry.secrets.headers || {}), updatedAt: entry.updatedAt,
      models: this.models(entry), maxConcurrent: entry.maxConcurrent || 1,
      enabled: entry.enabled !== false, accountID: this.account(state, entry) };
  }
  projection(summary) {
    return { id: summary.id, label: summary.label, revision: summary.revision, config: summary.config,
      models: summary.models, account_id: summary.accountID, max_concurrent: summary.maxConcurrent,
      enabled: summary.enabled, has_key: summary.hasKey, has_proxy: summary.hasProxy };
  }
  list() { return this.exclusive(async () => { const state = await this.read(); return state.entries.map(entry => this.summary(entry, state)); }); }
  prepare(input) { return this.exclusive(async () => this.prepareInState(input, await this.read())); }
  prepareInState(input, state) {
    if (!input || typeof input.label !== 'string' || !input.label.trim() || input.label.length > 128) throw new Error('请填写连接名称');
    if (Buffer.byteLength(JSON.stringify(input)) > 65536) throw new Error('连接配置过大');
    const old = state.entries.find(entry => entry.id === input.id);
    if (input.id && !old) throw new Error('连接不存在，请刷新列表');
    if (old && old.revision !== input.revision) throw new Error('连接已被修改，请刷新后重试');
    const config = structuredClone(input.config);
    if (!config || typeof config !== 'object') throw new Error('配置无效');
    const secrets = { key: input.clearKey ? '' : (input.key || old?.secrets.key || ''), proxy: input.clearProxy ? '' : (input.proxy || old?.secrets.proxy || ''), headers: {} };
    if (config.auth_kind === 'none') secrets.key = '';
    if (config.egress?.mode === 'system') { secrets.proxy = ''; delete config.proxy_env; }
    for (const [name, reference] of Object.entries(config.header_envs || {})) {
      if (typeof reference !== 'string') throw new Error('请求头引用无效');
      const value = input.headers?.[name] || old?.secrets.headers?.[name] || '';
      if (value) secrets.headers[name] = value;
    }
    for (const value of [secrets.key, secrets.proxy, ...Object.values(secrets.headers)]) {
      if (typeof value !== 'string' || value.length > 8192 || /[\r\n\0]/.test(value)) throw new Error('密钥、代理或请求头中包含无效字符');
    }
    const entry = { id: old?.id || crypto.randomUUID(), label: input.label.trim(), revision: (old?.revision || 0) + 1,
      config, secrets, updatedAt: new Date().toISOString(), models: input.models || old?.models,
      maxConcurrent: input.maxConcurrent ?? old?.maxConcurrent ?? 1, enabled: input.enabled ?? old?.enabled ?? true };
    const account = this.account(state, entry);
    const route = candidate => crypto.createHmac('sha256', Buffer.from(state.pepper, 'hex')).update(candidate.config.egress.mode + '\0' + candidate.secrets.proxy).digest('hex');
    if (state.entries.some(other => other.id !== entry.id && this.account(state, other) === account && route(other) !== route(entry))) throw new Error('同一 API 账号已有不同网络绑定，请先统一该账号的连接设置');
    return { entry, state, summary: this.summary(entry, state) };
  }
  save(input) {
    return this.exclusive(async () => {
      const prepared = this.prepareInState(input, await this.read());
      prepared.state.entries = prepared.state.entries.filter(entry => entry.id !== prepared.entry.id).concat(prepared.entry);
      await this.write(prepared.state);
      return prepared.summary;
    });
  }
  remove(id, revision) {
    return this.exclusive(async () => {
      const state = await this.read(), old = state.entries.find(entry => entry.id === id);
      if (!old || old.revision !== revision) throw new Error('连接已变化，请刷新后重试');
      state.entries = state.entries.filter(entry => entry.id !== id);
      await this.write(state);
      return { deleted: true };
    });
  }
  resolve(id, revision) {
    return this.exclusive(async () => {
      const state = await this.read(), entry = state.entries.find(entry => entry.id === id);
      if (!entry || entry.revision !== revision || entry.enabled === false) throw new Error('连接不存在、已停用或配置已变化');
      return { entry, summary: this.summary(entry, state), values: this.values(entry) };
    });
  }
  values(entry) {
    const values = {};
    if (entry.config.key_env) values[entry.config.key_env] = entry.secrets.key;
    if (entry.config.proxy_env) values[entry.config.proxy_env] = entry.secrets.proxy;
    for (const [name, reference] of Object.entries(entry.config.header_envs || {})) values[reference] = entry.secrets.headers[name] || '';
    return values;
  }
}
module.exports = { ConnectionStore };

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const { ConnectionStore } = require('./connections.cjs');
function encryption() {
  const key = crypto.randomBytes(32);
  return {
    isAsyncEncryptionAvailable: async () => true,
    getSelectedStorageBackend:()=> 'gnome_libsecret',
    encryptStringAsync: async text => {
      const iv = crypto.randomBytes(12), cipher = crypto.createCipheriv('aes-256-gcm', key, iv);
      const bytes = Buffer.concat([cipher.update(text, 'utf8'), cipher.final()]);
      return Buffer.concat([iv, cipher.getAuthTag(), bytes]);
    },
    decryptStringAsync: async bytes => {
      const cipher = crypto.createDecipheriv('aes-256-gcm', key, bytes.subarray(0, 12));
      cipher.setAuthTag(bytes.subarray(12, 28));
      return { result: Buffer.concat([cipher.update(bytes.subarray(28)), cipher.final()]).toString(), shouldReEncrypt: false };
    },
  };
}
const config = { endpoint: { protocol: 'messages', address: 'https://gateway.invalid', address_kind: 'base_url', allow_local_http: false }, model: 'custom-model', auth_kind: 'bearer', key_env: 'AWB_DESKTOP_KEY', egress: { mode: 'system', dns_mode: 'system' } };
function fixture(t) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'awb-connections-'));
  t.after(() => fs.rmSync(directory, { recursive: true }));
  const cipher = encryption();
  return { directory, cipher, store: new ConnectionStore(directory, cipher) };
}
test('异步加密保存、脱敏返回、空白保留和陈旧修改拒绝', async t => {
  const { directory, cipher, store } = fixture(t);
  const saved = await store.save({ label: '公司连接', config, key: 'SYNTHETIC_PRIVATE_KEY' });
  assert.equal(saved.hasKey, true);
  assert.equal(JSON.stringify(saved).includes('SYNTHETIC_PRIVATE_KEY'), false);
  assert.equal(fs.readFileSync(store.file).includes(Buffer.from('SYNTHETIC_PRIVATE_KEY')), false);
  const reloaded = new ConnectionStore(directory, cipher);
  assert.equal((await reloaded.list()).length, 1);
  const updated = await reloaded.save({ ...saved, key: '', label: '修改名称' });
  assert.equal(updated.hasKey, true);
  await assert.rejects(reloaded.save({ ...saved, label: '旧界面' }), /已被修改/);
  assert.equal((await reloaded.resolve(updated.id, updated.revision)).values.AWB_DESKTOP_KEY, 'SYNTHETIC_PRIVATE_KEY');
  await assert.rejects(new ConnectionStore(directory, { ...cipher, decryptStringAsync() { throw new Error(); } }).list(), /配置文件已保留/);
  assert.equal((await reloaded.list()).length, 1);
});
test('同账号别名不能用不同出口绕过网络绑定', async t => {
  const { store } = fixture(t);
  await store.save({ label: '连接一', config, key: 'SAME_SYNTHETIC_KEY' });
  await assert.rejects(store.prepare({ label: '连接二', config: { ...config, proxy_env: 'AWB_PROXY', egress: { mode: 'explicit_proxy', dns_mode: 'proxy' } }, key: 'SAME_SYNTHETIC_KEY', proxy: 'http://localhost:1080' }), /不同网络绑定/);
});
test('并发新增不丢数据，同修订修改只有一个成功，失败后队列仍可用', async t => {
  const { store } = fixture(t);
  const saved = await Promise.all(Array.from({ length: 8 }, (_, index) => store.save({ label: '连接' + index, config, key: 'SYNTHETIC_KEY_' + index })));
  assert.equal((await store.list()).length, 8);
  const changes = await Promise.allSettled([store.save({ ...saved[0], label: '修改一' }), store.save({ ...saved[0], label: '修改二' })]);
  assert.equal(changes.filter(result => result.status === 'fulfilled').length, 1);
  const latest = (await store.list()).find(entry => entry.id === saved[0].id);
  await store.remove(latest.id, latest.revision);
  assert.equal((await store.list()).length, 7);
});
test('拒绝系统加密缺失和 Linux 明文后端，失败不覆盖已有文件', async t => {
  const { directory, cipher, store } = fixture(t);
  const saved = await store.save({ label: '保留原配置', config, key: 'SYNTHETIC' });
  const previous = fs.readFileSync(store.file);
  const plaintext = new ConnectionStore(directory, { ...cipher, getSelectedStorageBackend: () => 'basic_text' },'linux');
  await assert.rejects(plaintext.save({ ...saved, label: '不可保存' }), /尚未就绪/);
  assert.deepEqual(fs.readFileSync(store.file), previous);
});

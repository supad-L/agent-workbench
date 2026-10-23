const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

function bridge() {
  let exposed;
  const calls = [];
  const electron = {
    contextBridge: { exposeInMainWorld: (name, value) => { assert.equal(name, 'workbench'); exposed = value; } },
    ipcRenderer: { invoke: (...args) => { calls.push(args); return Promise.resolve({ ok: true }); } },
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'preload.cjs'), 'utf8'), {
    require: name => { assert.equal(name, 'electron'); return electron; },
  });
  return { exposed, calls };
}

test('预加载层放行主进程已实现的每个操作并原样转交参数', async () => {
  const { exposed, calls } = bridge();
  const main = fs.readFileSync(path.join(__dirname, 'main.cjs'), 'utf8');
  const operations = [...main.matchAll(/case '([^']+)':/g)].map(match => match[1]);
  assert.ok(operations.includes('native.sessions.list'));
  assert.ok(operations.includes('native.sessions.get'));
  assert.ok(operations.includes('appearance.set'));
  for (const operation of operations) {
    const payload = { id: 'qa-project', session_id: 'qa-session' };
    assert.deepEqual(await exposed.invoke(operation, payload), { ok: true }, operation);
    assert.deepEqual(calls.at(-1), ['workbench:invoke', operation, payload]);
  }
  assert.equal(calls.length, operations.length);
});

test('预加载层拒绝未实现的操作且不发送 IPC', async () => {
  const { exposed, calls } = bridge();
  for (const operation of ['native.sessions.start', 'native.sessions.resume', 'accounts.refresh', 'accounts.auth.finish', 'arbitrary.command', '__proto__', undefined]) {
    await assert.rejects(exposed.invoke(operation), /操作不受支持/);
  }
  assert.equal(calls.length, 0);
});

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

test('启动就绪的多个回调仅输出一次验收报告和退出请求', () => {
  const output = [];
  let quits = 0;
  const app = { setName() {}, setPath() {}, getPath: () => '/synthetic-user-data', requestSingleInstanceLock: () => false, quit: () => quits++ };
  const context = vm.createContext({
    __dirname, Buffer, URL, setTimeout, clearTimeout, setImmediate,
    process: { argv: ['app', '--smoke-check'], env: {}, stdout: { write: value => output.push(value) } },
    require: name => name === 'electron' ? { app } : name.startsWith('./') ? require(path.join(__dirname, name)) : require(name),
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, 'main.cjs'), 'utf8'), context);
  const before = quits;
  vm.runInContext("checkStartup(); loaded=true; startupOperations.add('meta'); checkStartup(); startupOperations.add('projects.list'); checkStartup(); checkStartup(); checkStartup();", context);
  assert.equal(output.length, 1);
  assert.equal(JSON.parse(output[0]).status, 'passed');
  assert.equal(quits - before, 1);
});

const test = require('node:test');
const assert = require('node:assert/strict');
const { applyAppearance } = require('./appearance.cjs');

for (const [source, systemDark, expected] of [
  ['light', true, 'light'], ['dark', false, 'dark'],
  ['system', false, 'light'], ['system', true, 'dark'],
]) {
  test(`外观 ${source} / 系统${systemDark ? '深色' : '浅色'} 只更新应用外观`, () => {
    const changes = [];
    const nativeTheme = { themeSource: 'system', shouldUseDarkColors: systemDark };
    const window = { setBackgroundColor: value => changes.push(value) };
    const result = applyAppearance(source, { nativeTheme, window, platform: 'linux' });
    assert.deepEqual(result, { source, resolved: expected });
    assert.equal(nativeTheme.themeSource, source);
    assert.deepEqual(changes, [expected === 'dark' ? '#1e1e1e' : '#ffffff']);
  });
}

test('macOS 保持透明窗口背景以显示侧栏材质', () => {
  for (const source of ['light', 'dark', 'system']) {
    const changes = [];
    const nativeTheme = { themeSource: 'system', shouldUseDarkColors: true };
    applyAppearance(source, { nativeTheme, window: { setBackgroundColor: value => changes.push(value) }, platform: 'darwin' });
    assert.deepEqual(changes, ['#00000000']);
    assert.equal(nativeTheme.themeSource, source);
  }
});

test('无效外观输入在更改原生主题或窗口前拒绝', () => {
  const nativeTheme = { themeSource: 'light' };
  const window = { setBackgroundColor: () => assert.fail('invalid appearance changed window') };
  for (const invalid of [undefined, null, '', 'auto', {}, ['dark']]) {
    assert.throws(() => applyAppearance(invalid, { nativeTheme, window }), /外观模式无效/);
    assert.equal(nativeTheme.themeSource, 'light');
  }
});

import { afterEach, beforeEach, it, expect, vi } from 'vitest';
import { act, cleanup, render, screen, fireEvent, waitFor } from '@testing-library/react';
import { AppearanceProvider, AppearanceControl, appearanceStorageKey } from './appearance';
import Workbench from './workbench';

let query: { matches: boolean; addEventListener: ReturnType<typeof vi.fn>; removeEventListener: ReturnType<typeof vi.fn> };
let listeners: Set<() => void>;
beforeEach(() => {
  localStorage.clear(); listeners = new Set();
  query = { matches: false, addEventListener: vi.fn((_type, listener) => listeners.add(listener)), removeEventListener: vi.fn((_type, listener) => listeners.delete(listener)) };
  vi.spyOn(window, 'matchMedia').mockImplementation(() => query as unknown as MediaQueryList);
  window.workbench = { invoke: vi.fn(async operation => {
    if (operation === 'appearance.set') return { ok: true, value: {} };
    throw new Error(operation);
  }), onEvent: () => () => {} };
});
afterEach(() => {
  cleanup(); vi.restoreAllMocks(); localStorage.clear();
  delete document.documentElement.dataset.theme; document.documentElement.style.colorScheme = '';
});
async function choose(label: string) {
  fireEvent.mouseDown(screen.getByLabelText('外观模式'));
  fireEvent.click(await screen.findByRole('option', { name: label }));
}

it('记住手动选择，重新挂载时恢复，并只向桌面提交外观设置', async () => {
  const first = render(<AppearanceProvider><AppearanceControl /></AppearanceProvider>);
  await choose('深色');
  expect(document.documentElement.dataset.theme).toBe('dark');
  expect(document.documentElement.style.colorScheme).toBe('dark');
  expect(localStorage.getItem(appearanceStorageKey)).toBe('dark');
  expect(window.workbench.invoke).toHaveBeenLastCalledWith('appearance.set', { source: 'dark' });
  first.unmount();
  render(<AppearanceProvider><AppearanceControl /></AppearanceProvider>);
  expect(document.documentElement.dataset.theme).toBe('dark');
  expect(localStorage.length).toBe(1);
});

it('跟随系统动态变化，手动主题不被系统更新覆盖，卸载释放监听', async () => {
  const view = render(<AppearanceProvider><AppearanceControl /></AppearanceProvider>);
  expect(document.documentElement.dataset.theme).toBe('light');
  await act(async () => { query.matches = true; listeners.forEach(listener => listener()); });
  expect(document.documentElement.dataset.theme).toBe('dark');
  await choose('浅色');
  await act(async () => { query.matches = false; listeners.forEach(listener => listener()); query.matches = true; listeners.forEach(listener => listener()); });
  expect(document.documentElement.dataset.theme).toBe('light');
  await choose('跟随系统');
  expect(document.documentElement.dataset.theme).toBe('dark');
  view.unmount();
  expect(listeners.size).toBe(0);
});

it('在连接表单中切换主题保留未保存输入，不检查账号或请求模型', async () => {
  const calls: string[] = [];
  window.workbench.invoke = vi.fn(async operation => {
    calls.push(operation);
    if (operation === 'appearance.set') return { ok: true, value: {} };
    if (operation === 'meta') return { ok: true, value: { version: '0.1.0' } };
    if (operation === 'projects.list' || operation === 'connections.list') return { ok: true, value: [] };
    throw new Error(operation);
  });
  render(<AppearanceProvider><Workbench /></AppearanceProvider>);
  fireEvent.click(screen.getByRole('button', { name: '模型连接' }));
  const name = await screen.findByLabelText(/^连接名称/);
  fireEvent.change(name, { target: { value: '尚未保存的连接' } });
  await choose('深色');
  expect((screen.getByLabelText(/^连接名称/) as HTMLInputElement).value).toBe('尚未保存的连接');
  expect(document.documentElement.dataset.theme).toBe('dark');
  await waitFor(() => expect(calls).toContain('appearance.set'));
  expect(calls.some(operation => ['connections.save', 'connections.test', 'accounts.check', 'accounts.login'].includes(operation))).toBe(false);
});

it('无效存储值回退系统模式，存储不可写时仍切换并说明未保存', async () => {
  localStorage.setItem(appearanceStorageKey, 'unexpected-value');
  query.matches = true;
  render(<AppearanceProvider><AppearanceControl /></AppearanceProvider>);
  expect(document.documentElement.dataset.theme).toBe('dark');
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new DOMException('Storage unavailable', 'SecurityError'); });
  await choose('浅色');
  expect(document.documentElement.dataset.theme).toBe('light');
  expect(screen.getByRole('status').textContent).toContain('本次切换仍有效');
});

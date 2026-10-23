import { it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { App, ConfigProvider } from 'antd';
import Accounts from './accounts';

it('原生登录仅在点击时发起，Claude 在未验证强制出口时保持禁用', async () => {
  const codex = { provider: 'codex', name: 'GPT / ChatGPT', installed: true, loggedIn: false, checked: false, loginReady: true, reason: '隔离执行待验证' };
  const invoke = vi.fn(async (operation, payload) => {
    if (operation === 'accounts.list') return { ok: true, value: [codex, { provider: 'claude', name: 'Claude 订阅', installed: true, loginReady: false, reason: '强制住宅出口未验证' }] };
    if (operation === 'accounts.login') { expect(payload).toEqual({ provider: 'codex' }); return { ok: true, value: { ...codex, loginPending: true } }; }
    throw new Error(operation);
  });
  window.workbench = { invoke, onEvent: () => () => {} };
  render(<ConfigProvider><App><Accounts /></App></ConfigProvider>);
  const login = await screen.findByRole('button', { name: '登录 ChatGPT' });
  expect(invoke.mock.calls.filter(([operation]) => operation === 'accounts.login')).toHaveLength(0);
  expect((screen.getByRole('button', { name: '等待强制出口验证后开放登录' }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(login);
  await screen.findByText('等待浏览器授权');
  await waitFor(() => expect(invoke.mock.calls.filter(([operation]) => operation === 'accounts.login')).toHaveLength(1));
});

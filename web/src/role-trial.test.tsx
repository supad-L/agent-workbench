import { it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { App, ConfigProvider } from 'antd';
import RoleTrial from './role-trial';

it('角色试运行只传连接引用与用户选定资料范围，不从界面提交凭证', async () => {
  const calls: any[] = [];
  window.workbench = { invoke: vi.fn(async (operation, payload: any) => {
    calls.push({ operation, payload });
    if (operation === 'projects.list') return { ok: true, value: [{ id: 'project', name: '验收项目' }] };
    if (operation === 'runs.list') return { ok: true, value: [] };
    if (operation === 'runs.start') return { ok: true, value: { id: 'run', role: { id: 'role' }, status: 'completed', state: { completed: true, text: '只读试运行结果', attempts: 1 }, created_at: new Date().toISOString() } };
    throw new Error(operation);
  }), onEvent: () => () => {} };
  render(<ConfigProvider><App><RoleTrial role={{ id: 'role', revision: 3, name: '前端工程师' }} onClose={() => {}} /></App></ConfigProvider>);
  await waitFor(() => expect(calls.some(call => call.operation === 'projects.list')).toBe(true));
  fireEvent.mouseDown(screen.getByLabelText('关联项目'));
  fireEvent.click(await screen.findByRole('option', { name: '验收项目' }));
  fireEvent.change(screen.getByLabelText('试运行输入'), { target: { value: '说明你的职责和工作边界。' } });
  fireEvent.click(screen.getByRole('button', { name: '开始试运行（消耗 API 额度）' }));
  await waitFor(()=>expect(calls.some(call=>call.operation==='runs.start'),JSON.stringify(calls)).toBe(true));
  await waitFor(() => expect((screen.getByLabelText('试运行输出') as HTMLTextAreaElement).value).toBe('只读试运行结果'));
  const start = calls.find(call => call.operation === 'runs.start');
  expect(start.payload.role_revision).toBe(3);
  expect(start.payload.scope).toBe('conversation_only');
  expect(start.payload.values).toBeUndefined();
  expect(start.payload.config).toBeUndefined();
});

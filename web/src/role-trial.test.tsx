import { it, expect, vi } from 'vitest';
import { act, render, screen, fireEvent, waitFor } from '@testing-library/react';
import { App, ConfigProvider } from 'antd';
import RoleTrial from './role-trial';

it('角色试运行只传连接引用与用户选定资料范围，不从界面提交凭证', async () => {
  const calls: any[] = [];
  window.workbench = { invoke: vi.fn(async (operation, payload: any) => {
    calls.push({ operation, payload });
    if (operation === 'projects.list') return { ok: true, value: [{ id: 'project', name: '验收项目' }] };
    if (operation === 'events.start' || operation === 'events.stop') return { ok: true, value: {} };
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

const roleFixture = { id: 'role', revision: 3, name: '前端工程师', primary: { model_id: 'current-model' } };
const completedRun = (id = 'run'): any => ({
  id, project_id: 'project', role: roleFixture, status: 'completed', scope: 'project_summary',
  choice_index: 0, choices: [{ connection: { label: '本次连接快照' }, model: { id: 'saved-model' } }],
  input: '已保存的输入', state: { completed: true, text: `${id} 输出`, attempts: 2, next_turn: 2,
    tool_results: { 'call-1': { name: 'read_plan', text: '计划摘要' } }, pending: [] },
  max_turns: 4, retries: 1, created_at: '2026-10-23T01:00:00Z', updated_at: '2026-10-23T01:00:02Z',
});
async function selectProject() {
  fireEvent.mouseDown(screen.getByLabelText('关联项目'));
  fireEvent.click(await screen.findByRole('option', { name: '验收项目' }));
}
async function startFixtureTrial() {
  await selectProject();
  fireEvent.change(screen.getByLabelText('试运行输入'), { target: { value: '说明状态' } });
  fireEvent.click(screen.getByRole('button', { name: '开始试运行（消耗 API 额度）' }));
  await screen.findByLabelText('本次试运行详情');
}

it('持久事件按项目和运行隔离、按序去重，快照与工具可交互查看', async () => {
  let listener: (event: any) => void = () => {};
  const off = vi.fn();
  const calls: string[] = [];
  window.workbench = { invoke: vi.fn(async operation => {
    calls.push(operation);
    if (operation === 'projects.list') return { ok: true, value: [{ id: 'project', name: '验收项目' }] };
    if (operation === 'runs.list') return { ok: true, value: [] };
    if (operation === 'runs.start') return { ok: true, value: completedRun() };
    if (operation === 'events.start' || operation === 'events.stop') return { ok: true, value: {} };
    throw new Error(operation);
  }), onEvent: callback => { listener = callback; return off; } };
  const view = render(<ConfigProvider><App><RoleTrial role={roleFixture} onClose={() => {}} /></App></ConfigProvider>);
  await startFixtureTrial();
  await screen.findByText('saved-model');
  expect(screen.getByText('本次连接快照')).toBeTruthy();
  const event = (sequence: number, type: string) => ({ sequence, project_id: 'project', kind: 'run.progress', payload: { run_id: 'run', type, tool: 'read_plan' }, created_at: '2026-10-23T01:00:01Z' });
  await act(async () => {
    listener(event(3, 'tool_finished'));
    listener(event(2, 'tool_started'));
    listener(event(2, 'tool_started'));
    listener({ ...event(4, 'retry_wait'), project_id: 'other-project' });
    listener({ ...event(5, 'retry_wait'), payload: { run_id: 'other-run', type: 'retry_wait' } });
  });
  fireEvent.click(screen.getByRole('tab', { name: '运行事件 (2)' }));
  expect(screen.getAllByText('调用工具：读取确认方案')).toHaveLength(1);
  expect(screen.queryByText('等待有限重试')).toBeNull();
  const labels = [...document.querySelectorAll('.trial-timeline li>span')].map(item => item.textContent);
  expect(labels).toEqual(['调用工具：读取确认方案', '工具已返回：读取确认方案']);
  fireEvent.click(screen.getByRole('tab', { name: '只读工具 (1)' }));
  await screen.findByText('返回已记录');
  expect(calls.filter(operation => operation === 'runs.start')).toHaveLength(1);
  view.unmount();
  expect(off).toHaveBeenCalled();
  expect(calls.includes('events.stop')).toBe(true);
});

it('事件订阅失败可重试，重新订阅和回放不增加模型请求', async () => {
  let listener: (event: any) => void = () => {};
  let subscriptions = 0, starts = 0;
  window.workbench = { invoke: vi.fn(async operation => {
    if (operation === 'projects.list') return { ok: true, value: [{ id: 'project', name: '验收项目' }] };
    if (operation === 'runs.list') return { ok: true, value: [] };
    if (operation === 'runs.start') { starts++; return { ok: true, value: completedRun() }; }
    if (operation === 'events.stop') return { ok: true, value: {} };
    if (operation === 'events.start') {
      subscriptions++;
      if (subscriptions === 1) return { ok: false, error: '本地订阅暂不可用' };
      listener({ sequence: 1, project_id: 'project', kind: 'run.started', payload: { run_id: 'run' }, created_at: '2026-10-23T01:00:00Z' });
      return { ok: true, value: {} };
    }
    throw new Error(operation);
  }), onEvent: callback => { listener = callback; return () => {}; } };
  render(<ConfigProvider><App><RoleTrial role={roleFixture} onClose={() => {}} /></App></ConfigProvider>);
  await startFixtureTrial();
  fireEvent.click(screen.getByRole('tab', { name: '运行事件 (0)' }));
  fireEvent.click(await screen.findByRole('button', { name: '重新订阅事件' }));
  await screen.findByText('开始试运行');
  expect(subscriptions).toBe(2);
  expect(starts).toBe(1);
  expect((screen.getByLabelText('试运行输出') as HTMLTextAreaElement).value).toBe('run 输出');
});

it('切换历史记录后，晚到的旧详情不能覆盖当前选择', async () => {
  const first = { ...completedRun('first'), status: 'cancelled' };
  const second = completedRun('second');
  let resolveFirst!: (value: any) => void;
  window.workbench = { invoke: vi.fn(async (operation, payload: any) => {
    if (operation === 'projects.list') return { ok: true, value: [{ id: 'project', name: '验收项目' }] };
    if (operation === 'runs.list') return { ok: true, value: [first, second] };
    if (operation === 'runs.get') {
      if (payload.run_id === 'first') return new Promise<{ ok: boolean; value: unknown }>(resolve => { resolveFirst = resolve; });
      return { ok: true, value: second };
    }
    if (operation === 'events.start' || operation === 'events.stop') return { ok: true, value: {} };
    throw new Error(operation);
  }), onEvent: () => () => {} };
  render(<ConfigProvider><App><RoleTrial role={roleFixture} onClose={() => {}} /></App></ConfigProvider>);
  await selectProject();
  const history = await screen.findByLabelText('历史试运行');
  fireEvent.mouseDown(history);
  fireEvent.click(await screen.findByRole('option', { name: /已取消/ }));
  await waitFor(() => expect(resolveFirst).toBeTypeOf('function'));
  fireEvent.mouseDown(history);
  fireEvent.click(await screen.findByRole('option', { name: /已完成试运行/ }));
  await waitFor(() => expect((screen.getByLabelText('试运行输出') as HTMLTextAreaElement).value).toBe('second 输出'));
  await act(async () => { resolveFirst({ ok: true, value: first }); });
  expect((screen.getByLabelText('试运行输出') as HTMLTextAreaElement).value).toBe('second 输出');
});

it('折叠输入后仍可取消运行并显式继续，历史展示不会另建运行', async () => {
  let cancelled = false;
  const calls: string[] = [];
  const active = { ...completedRun(), status: 'running', state: { completed: false, text: '', attempts: 1, next_turn: 0, tool_results: {}, pending: [] } };
  window.workbench = { invoke: vi.fn(async (operation, payload: any) => {
    calls.push(operation);
    if (operation === 'projects.list') return { ok: true, value: [{ id: 'project', name: '验收项目' }] };
    if (operation === 'runs.list') return { ok: true, value: [] };
    if (operation === 'runs.start') return { ok: true, value: active };
    if (operation === 'events.start' || operation === 'events.stop') return { ok: true, value: {} };
    if (operation === 'runs.cancel') { expect(payload).toEqual({ id: 'project', run_id: 'run' }); cancelled = true; return { ok: true, value: { cancel_requested: true } }; }
    if (operation === 'runs.get') return { ok: true, value: cancelled ? { ...active, status: 'cancelled' } : active };
    if (operation === 'runs.resume') return { ok: true, value: completedRun() };
    throw new Error(operation);
  }), onEvent: () => () => {} };
  render(<ConfigProvider><App><RoleTrial role={roleFixture} onClose={() => {}} /></App></ConfigProvider>);
  await startFixtureTrial();
  fireEvent.click(screen.getByRole('button', { name: '取消运行' }));
  fireEvent.click(await screen.findByRole('button', { name: '继续运行（可能再次计费）' }));
  await waitFor(() => expect((screen.getByLabelText('试运行输出') as HTMLTextAreaElement).value).toBe('run 输出'));
  expect(calls.filter(operation => operation === 'runs.start')).toHaveLength(1);
  expect(calls.filter(operation => operation === 'runs.cancel')).toHaveLength(1);
  expect(calls.filter(operation => operation === 'runs.resume')).toHaveLength(1);
});

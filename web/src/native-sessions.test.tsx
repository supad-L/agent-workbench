import { it, expect, vi } from 'vitest';
import { act, render, screen, fireEvent } from '@testing-library/react';
import { App, ConfigProvider } from 'antd';
import NativeSessions from './native-sessions';

it('原生会话显示未知结果与本地恢复条件，读取检查点不会启动或续接', async () => {
  const session = { id: 'native-one', status: 'uncertain', recovery: 'uncertain', native_session_id: 'provider-session', run_directory: '/private/synthetic/native-one', binding: { model: 'saved-model', runtime: 'podman' }, attempt: 1, revision: 4, updated_at: '2026-10-23T01:00:00Z', cleanup_verified: true, checkpoint: { text: '保存的草稿', provider_completed: true } };
  const view = { session, can_resume: false, recovery_reason: '结果未知，先核对运行与成果；不能自动重发', live_ready: false };
  const operations: string[] = [];
  window.workbench = { invoke: vi.fn(async (operation, payload: any) => {
    operations.push(operation);
    if (operation === 'native.sessions.list') return { ok: true, value: [view] };
    if (operation === 'native.sessions.get') { expect(payload).toEqual({ id: 'project', session_id: 'native-one' }); return { ok: true, value: { view, attempts: [{ number: 1, mode: 'start', status: 'uncertain', checkpoint_sequence: 4, checkpoint: session.checkpoint }] } }; }
    throw new Error(operation);
  }), onEvent: () => () => {} };
  render(<ConfigProvider><App><NativeSessions projectID="project" /></App></ConfigProvider>);
  await screen.findByText('结果未知');
  fireEvent.click(screen.getByRole('button', { name: '查看检查点' }));
  await screen.findByText('provider-session');
  expect((screen.getByLabelText('原生检查点草稿') as HTMLTextAreaElement).value).toBe('保存的草稿');
  expect((screen.getByRole('button', { name: '账号与在线隔离执行接通后开放继续' }) as HTMLButtonElement).disabled).toBe(true);
  expect(operations).toEqual(['native.sessions.list', 'native.sessions.get']);
});

it('本地续接条件齐备单独显示，不作为真实账号执行已就绪', async () => {
  window.workbench = { invoke: vi.fn(async () => ({ ok: true, value: [{ session: { id: 'one', binding: { model: 'fixture', runtime: 'podman' }, status: 'interrupted', recovery: 'resumable' }, can_resume: true, live_ready: false, recovery_reason: '仅本地文件条件' }] })), onEvent: () => () => {} };
  render(<ConfigProvider><App><NativeSessions projectID="project" /></App></ConfigProvider>);
  await screen.findByText('本地续接条件齐备');
  expect(screen.getByText('已中断')).toBeTruthy();
  expect(screen.getByText(/真实账号与在线隔离执行仍待验收/)).toBeTruthy();
});

it('切换项目时隐藏旧记录，晚到响应不覆盖新项目', async () => {
  let resolveOld!: (value: any) => void;
  const old = { session: { id: 'old', binding: { model: 'old-model' }, status: 'uncertain', recovery: 'uncertain' }, can_resume: false };
  window.workbench = { invoke: vi.fn(async (_operation, payload: any) => {
    if (payload.id === 'old-project') return new Promise<{ ok: boolean; value: unknown }>(resolve => { resolveOld = resolve; });
    return { ok: true, value: [{ session: { id: 'new', binding: { model: 'new-model' }, status: 'ready', recovery: 'not_started' }, can_resume: false }] };
  }), onEvent: () => () => {} };
  const view = render(<ConfigProvider><App><NativeSessions projectID="old-project" /></App></ConfigProvider>);
  view.rerender(<ConfigProvider><App><NativeSessions projectID="new-project" /></App></ConfigProvider>);
  await screen.findByText('new-model');
  await act(async () => { resolveOld({ ok: true, value: [old] }); });
  expect(screen.queryByText('old-model')).toBeNull();
  expect(screen.getByText('new-model')).toBeTruthy();
});

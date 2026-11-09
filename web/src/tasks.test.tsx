import { it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { App, ConfigProvider } from 'antd';
import Tasks, { taskEligibility } from './tasks';

const project = { id: 'project', name: '演示项目', source: '', snapshot: '/managed', manifest: [{ path: 'src', kind: 'directory' }, { path: 'src/app.js', kind: 'file' }, { path: '.env', kind: 'file', excluded: '可能含密钥' }], active_version: 0, created_at: '' } as any;
const connection = { id: 'deepseek', label: 'DeepSeek', revision: 2, enabled: true, models: [{ id: 'deepseek-chat', tools: 'supported' }] };
const eligible = { id: 'frontend', name: '前端工程师', revision: 3, enabled: true, tools: ['read_file', 'write_file'], primary: { connection_id: 'deepseek', model_id: 'deepseek-chat' } };
const readOnly = { id: 'reviewer', name: '审查员', revision: 1, enabled: true, tools: ['read_file'], primary: { connection_id: 'deepseek', model_id: 'deepseek-chat' } };
const mount = () => render(<ConfigProvider><App><Tasks project={project} /></App></ConfigProvider>);

it('新建任务只提交角色与目录引用，不提交凭证或连接配置', async () => {
  const calls: { operation: string; payload: any }[] = [];
  const started = { id: 'task-1', kind: 'task', status: 'running', role: eligible, choices: [{ connection, model: connection.models[0] }], choice_index: 0, max_turns: 30, retries: 1, created_at: '2026-11-09T00:00:00Z',
    task: { title: '修正答案', write_dirs: ['src/'], changes: [] }, state: { attempts: 0, tool_results: {} } };
  window.workbench = { invoke: vi.fn(async (operation: string, payload: any) => {
    calls.push({ operation, payload });
    if (operation === 'tasks.list') return { ok: true, value: calls.some(call => call.operation === 'tasks.start') ? [started] : [] };
    if (operation === 'roles.list') return { ok: true, value: [eligible, readOnly] };
    if (operation === 'connections.list') return { ok: true, value: [connection] };
    if (operation === 'tasks.start') return { ok: true, value: started };
    if (operation === 'tasks.changes') return { ok: true, value: { files: [] } };
    throw new Error(operation);
  }), onEvent: () => () => {} };
  mount();
  await screen.findByText('新建开发任务');
  fireEvent.change(screen.getByLabelText('任务标题'), { target: { value: '修正答案' } });
  fireEvent.change(screen.getByLabelText('任务说明'), { target: { value: '把 answer 改成 42' } });
  fireEvent.mouseDown(screen.getByLabelText('执行角色'));
  expect(await screen.findByText('审查员（需要启用读取与写入任务文件）')).toBeTruthy();
  fireEvent.click(await screen.findByRole('option', { name: '前端工程师' }));
  fireEvent.mouseDown(screen.getByLabelText('允许修改的目录'));
  fireEvent.click(await screen.findByRole('option', { name: 'src/' }));
  expect(screen.getByText(/会发送给“DeepSeek”/)).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: '开始执行（消耗 API 额度）' }));
  await waitFor(() => expect(calls.some(call => call.operation === 'tasks.start')).toBe(true));
  const start = calls.find(call => call.operation === 'tasks.start')!.payload;
  expect(start.role_id).toBe('frontend');
  expect(start.role_revision).toBe(3);
  expect(start.write_dirs).toEqual(['src/']);
  expect(start.runtime).toBe('');
  expect(start.image).toBe('');
  expect(start.values).toBeUndefined();
  expect(start.config).toBeUndefined();
  expect(typeof start.idempotency_key).toBe('string');
  await screen.findByRole('heading', { name: '修正答案' });
  expect(calls.filter(call => call.operation === 'tasks.start')).toHaveLength(1);
});

it('结束的任务显示逐行差异并可导出补丁，查看不会重新执行', async () => {
  const calls: string[] = [];
  const done = { id: 'task-2', kind: 'task', status: 'completed', role: eligible, choices: [{ connection, model: connection.models[0] }], choice_index: 0, max_turns: 30, retries: 1, created_at: '2026-11-09T00:00:00Z',
    task: { title: '改答案', write_dirs: ['src/'], changes: [{ path: 'src/app.js', kind: 'modified' }] },
    state: { completed: true, text: '已修改答案', attempts: 2, tool_results: { w1: { name: 'replace_in_file', arguments: { path: 'src/app.js' }, text: '已替换 src/app.js 中的一处内容' } } } };
  const text = 'diff --git a/src/app.js b/src/app.js\n--- a/src/app.js\n+++ b/src/app.js\n@@ -1 +1 @@\n-export const answer = 41;\n+export const answer = 42;\n';
  window.workbench = { invoke: vi.fn(async (operation: string) => {
    calls.push(operation);
    if (operation === 'tasks.list') return { ok: true, value: [done] };
    if (operation === 'roles.list') return { ok: true, value: [eligible] };
    if (operation === 'connections.list') return { ok: true, value: [connection] };
    if (operation === 'tasks.changes') return { ok: true, value: { files: [{ path: 'src/app.js', kind: 'modified', added: 1, removed: 1, text }] } };
    if (operation === 'tasks.exportPatch') return { ok: true, value: { saved: '/tmp/x.patch', files: 1 } };
    throw new Error(operation);
  }), onEvent: () => () => {} };
  mount();
  await screen.findByRole('heading', { name: '改答案' });
  expect(await screen.findByText('export const answer = 42;')).toBeTruthy();
  expect(screen.getByText('export const answer = 41;')).toBeTruthy();
  expect(screen.getByText('改动 (1)')).toBeTruthy();
  fireEvent.click(screen.getByRole('button', { name: '导出补丁' }));
  await waitFor(() => expect(calls).toContain('tasks.exportPatch'));
  fireEvent.click(screen.getByRole('tab', { name: '模型说明' }));
  expect(await screen.findByText('已修改答案')).toBeTruthy();
  expect(calls.some(operation => ['tasks.start', 'runs.resume', 'runs.start'].includes(operation))).toBe(false);
});

it('角色资格检查覆盖停用、缺少工具、连接与模型能力', () => {
  expect(taskEligibility(eligible, [connection])).toBe('');
  expect(taskEligibility({ ...eligible, enabled: false }, [connection])).toBe('角色已停用');
  expect(taskEligibility(readOnly, [connection])).toContain('写入');
  expect(taskEligibility({ ...eligible, tools: ['read_file', 'apply_patch'] }, [connection])).toBe('');
  expect(taskEligibility(eligible, [])).toBe('尚未绑定模型连接');
  expect(taskEligibility(eligible, [{ ...connection, enabled: false }])).toBe('绑定连接已停用');
  expect(taskEligibility(eligible, [{ ...connection, models: [{ id: 'deepseek-chat', tools: 'unsupported' }] }])).toContain('不支持');
});

import { it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { App, ConfigProvider } from 'antd';
import Workbench from './workbench';
import Connections from './connections';

const mount = (view: React.ReactNode) => render(<ConfigProvider><App>{view}</App></ConfigProvider>);
const fill = () => {
  fireEvent.change(screen.getByLabelText(/^连接名称/), { target: { value: '公司 API' } });
  fireEvent.change(screen.getByLabelText(/^服务地址/), { target: { value: 'https://gateway.invalid' } });
  fireEvent.change(screen.getByLabelText(/^模型 ID/), { target: { value: 'company-model' } });
};

it('接入方式跨页定位账号，导航不会触发登录、状态检查或付费测试', async () => {
  const calls: string[] = [];
  window.workbench = { invoke: vi.fn(async (operation) => {
    calls.push(operation);
    if (operation === 'meta') return { ok: true, value: { version: '0.1.0' } };
    if (operation === 'projects.list' || operation === 'connections.list') return { ok: true, value: [] };
    if (operation === 'accounts.list') return { ok: true, value: [
      { provider: 'codex', name: 'GPT / ChatGPT', installed: true, loggedIn: true, checked: true, loginReady: true, executionReady: false, reason: '真实隔离编码尚未验收' },
      { provider: 'claude', name: 'Claude 订阅', installed: false, loginReady: false, executionReady: false, reason: '强制住宅出口尚未验证' },
    ] };
    throw new Error(`unexpected ${operation}`);
  }), onEvent: () => () => {} };
  mount(<Workbench />);
  fireEvent.click(screen.getByRole('button', { name: '模型连接' }));
  await screen.findByRole('navigation', { name: '选择接入方式' });
  fireEvent.click(screen.getByRole('button', { name: '查看 Claude 接入条件' }));
  const blocked = await screen.findByRole('button', { name: '等待强制出口验证后开放登录' });
  expect((blocked as HTMLButtonElement).disabled).toBe(true);
  await waitFor(() => expect(document.activeElement?.id).toBe('account-claude'));
  expect(screen.getAllByText('尚未就绪')).toHaveLength(2);
  fireEvent.click(screen.getByRole('button', { name: '管理 GPT 账号' }));
  await waitFor(() => expect(document.activeElement?.id).toBe('account-codex'));
  fireEvent.click(screen.getByRole('button', { name: '配置 API 连接' }));
  await screen.findByText('新增自定义连接');
  expect(calls.filter(operation => ['accounts.login', 'accounts.check', 'connections.test'].includes(operation))).toEqual([]);
});

it('配置保存与工具验证分别显示，修改后撤销当前表单证据', async () => {
  let entry: any;
  const calls: string[] = [];
  const onRoles = vi.fn();
  window.workbench = { invoke: vi.fn(async (operation, payload: any) => {
    calls.push(operation);
    if (operation === 'connections.list') return { ok: true, value: entry ? [entry] : [] };
    if (operation === 'connections.save') {
      entry = { ...payload, id: 'company', revision: 1, hasKey: false, hasProxy: false };
      return { ok: true, value: entry };
    }
    if (operation === 'connections.test') return { ok: true, value: { requests: 2, usage_known: false } };
    throw new Error(operation);
  }), onEvent: () => () => {} };
  mount(<Connections onRoles={onRoles} />);
  await screen.findByText('新增自定义连接');
  fill();
  const rolesButton = screen.getByRole('button', { name: '前往配置角色' });
  expect((rolesButton as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole('button', { name: /保存连接/ }));
  await screen.findByText('已加密保存');
  expect(calls.filter(operation => operation === 'connections.test')).toHaveLength(0);
  fireEvent.click(rolesButton);
  expect(onRoles).toHaveBeenCalledOnce();
  fireEvent.click(screen.getByRole('button', { name: '测试连接' }));
  await screen.findByText('本次工具验证通过');
  fireEvent.change(screen.getByLabelText(/^模型 ID/), { target: { value: 'different-model' } });
  await screen.findByText('有未保存修改');
  expect(screen.queryByText('本次工具验证通过')).toBeNull();
  expect((rolesButton as HTMLButtonElement).disabled).toBe(true);
});

it('模型测试返回较晚时，不给后来编辑的配置显示通过', async () => {
  let resolveTest!: (result: any) => void;
  const calls: string[] = [];
  window.workbench = { invoke: vi.fn(async (operation) => {
    calls.push(operation);
    if (operation === 'connections.list') return { ok: true, value: [] };
    if (operation === 'connections.test') return new Promise<{ ok: boolean; value: unknown }>(resolve => { resolveTest = resolve; });
    throw new Error(operation);
  }), onEvent: () => () => {} };
  mount(<Connections />);
  await screen.findByText('新增自定义连接');
  fill();
  fireEvent.click(screen.getByRole('button', { name: '测试连接' }));
  await waitFor(() => expect(resolveTest).toBeTypeOf('function'));
  fireEvent.change(screen.getByLabelText(/^模型 ID/), { target: { value: 'later-model' } });
  resolveTest({ ok: true, value: { requests: 2, usage_known: true } });
  await waitFor(() => expect(screen.getByRole('button', { name: /保存连接/ }).getAttribute('disabled')).toBeNull());
  expect(screen.queryByText('连接测试通过')).toBeNull();
  expect(screen.queryByText('本次工具验证通过')).toBeNull();
  expect((screen.getByLabelText(/^模型 ID/) as HTMLInputElement).value).toBe('later-model');
  expect(calls.filter(operation => operation === 'connections.test')).toHaveLength(1);
});

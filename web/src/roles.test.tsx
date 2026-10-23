import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { App, ConfigProvider } from 'antd';
import Roles from './roles';

describe('Agent 角色卡片', () => {
  it('从模板创建角色，绑定连接与模型，保持默认备用策略关闭', async () => {
    let roles: any[] = [];
    const template = { id: 'frontend', revision: 0, name: '前端工程师', kind: 'frontend', description: '界面与交互', system_prompt: '只执行固定方案，不增加审批。', primary: { connection_id: '', model_id: '' }, tools: ['read_plan'], enabled: false, fallback: { enabled: false, candidates: [], data_scope: 'conversation_only', max_extra_cost_micros: 0 } };
    const invoke = vi.fn(async (operation: string, payload: any) => {
      if (operation === 'roles.list') return { ok: true, value: structuredClone(roles) };
      if (operation === 'connections.list') return { ok: true, value: [{ id: 'company', label: '公司连接', enabled: true, models: [{ id: 'custom-model', tools: 'supported' }] }] };
      if (operation === 'roles.templates') return { ok: true, value: { templates: [template], tools: ['read_plan', 'read_file'] } };
      if (operation === 'roles.save') { roles = [{ ...payload.role, revision: 1 }]; return { ok: true, value: roles[0] }; }
      throw new Error(operation);
    });
    window.workbench = { invoke, onEvent: () => () => {} };
    render(<ConfigProvider><App><Roles /></App></ConfigProvider>);
    fireEvent.click(await screen.findByRole('button', { name: /前端工程师.*从模板新建/ }));
    expect((screen.getByLabelText('系统角色提示词') as HTMLTextAreaElement).value).toContain('不增加审批');
    expect(invoke.mock.calls.some(([name]) => name === 'roles.save')).toBe(false);
    fireEvent.mouseDown(screen.getByLabelText('使用的连接'));
    fireEvent.click(await screen.findByText('公司连接'));
    fireEvent.mouseDown(screen.getByLabelText('使用的模型'));
    fireEvent.click(await screen.findByRole('option', { name: 'custom-model' }));
    fireEvent.click(screen.getByRole('button', { name: '保存角色' }));
    await waitFor(() => expect(roles).toHaveLength(1));
    expect(roles[0].primary).toEqual({ connection_id: 'company', model_id: 'custom-model' });
    expect(roles[0].fallback.enabled).toBe(false);
    expect(roles[0].enabled).toBe(false);
    await screen.findByRole('heading', { name: '前端工程师' });
  });
});

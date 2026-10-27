import { it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { App, ConfigProvider } from 'antd';
import NativeEnvironment from './native-environment';

it('仅显式点击检测，不请求登录或模型；改动配置后清除旧结果', async () => {
  const invoke=vi.fn(async (operation,payload) => {
    expect(operation).toBe('native.environment.check');
    expect(payload).toEqual({ provider:'codex',runtime:'podman',image:'fixture-image',binary:'/usr/local/bin/codex',scratch:'/fixture',mode:'system',target:'',proxy:'' });
    return { ok:true,value:{ checked_at:'2026-10-27T00:00:00Z',environment_ready:false,execution_ready:false,checks:[{ name:'network',status:'skipped',detail:'未填写目标，未发起请求' }] } };
  });
  window.workbench={ invoke,onEvent:()=>()=>{} };
  render(<ConfigProvider><App><NativeEnvironment /></App></ConfigProvider>);
  expect(invoke).not.toHaveBeenCalled();
  fireEvent.mouseDown(screen.getByRole('combobox',{ name:'容器运行时' }));
  fireEvent.click(await screen.findByText('Podman'));
  fireEvent.change(screen.getByLabelText('本地镜像名称或 ID'),{ target:{ value:'fixture-image' } });
  fireEvent.change(screen.getByPlaceholderText('仅在此目录中新建临时夹具，不挂载项目或账号'),{ target:{ value:'/fixture' } });
  fireEvent.click(screen.getByRole('button',{ name:'检测执行环境' }));
  fireEvent.click(screen.getByRole('button',{ name:'检测执行环境' }));
  await screen.findByRole('region',{ name:'执行环境检测结果' }).catch(() => screen.findByText('未填写目标，未发起请求'));
  await waitFor(()=>expect(invoke).toHaveBeenCalledTimes(1));
  expect(screen.getByText('原生编码尚未开放')).toBeTruthy();
  fireEvent.change(screen.getByLabelText('本地镜像名称或 ID'),{ target:{ value:'changed' } });
  await waitFor(()=>expect(screen.queryByText('原生编码尚未开放')).toBeNull());
});

it('选择目录不触发检测；供应商切换会忽略晚到的目录结果', async () => {
  let resolve!:(value:any)=>void;
  window.workbench={ invoke:vi.fn(()=>new Promise<{ok:boolean;value?:unknown}>(done=>{resolve=done;})),onEvent:()=>()=>{} };
  const wrap=(provider:string)=><ConfigProvider><App><NativeEnvironment provider={provider} /></App></ConfigProvider>;
  const view=render(wrap('codex'));
  fireEvent.click(screen.getByRole('button',{ name:'选择目录' }));
  expect(window.workbench.invoke).toHaveBeenCalledWith('choose.directory', {});
  view.rerender(wrap('claude'));
  resolve({ ok:true,value:'/old-provider-directory' });
  await waitFor(()=>expect((screen.getByLabelText('容器内工具路径') as HTMLInputElement).value).toBe('/usr/local/bin/claude'));
  expect((screen.getByPlaceholderText('仅在此目录中新建临时夹具，不挂载项目或账号') as HTMLInputElement).value).toBe('');
});

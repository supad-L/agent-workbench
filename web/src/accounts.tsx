import { useEffect, useState } from 'react';
import { App, Alert, Button, Card, Popconfirm, Space, Tag } from 'antd';
import { api } from './api';
import { ConnectionAccess, type AccountProvider } from './connection-guide';

export default function Accounts({ provider, onConnections, onAccounts }: { provider?: AccountProvider; onConnections?: () => void; onAccounts?: (provider: AccountProvider) => void } = {}) {
  const { message } = App.useApp();
  const [accounts, setAccounts] = useState<any[]>([]);
  const [busy, setBusy] = useState('');
  useEffect(() => { api<any[]>('accounts.list').then(setAccounts).catch(error => message.error(error.message)); }, []);
  useEffect(() => {
    if (!provider) return;
    const target = document.getElementById(`account-${provider}`);
    target?.scrollIntoView({ block: 'nearest' });
    target?.focus({ preventScroll: true });
  }, [provider, accounts.length]);
  const pending = accounts.some(account => account.loginPending);
  useEffect(() => {
    if (!pending) return;
    let disposed = false;
    let timer:ReturnType<typeof setTimeout>;
    async function refresh(){try{const result=await api<any>('accounts.check',{provider:'codex'});if(!disposed){setAccounts(current=>current.map(account=>account.provider==='codex'?result:account));if(result.loginPending)timer=setTimeout(refresh,2000)}}catch(error:any){if(!disposed)message.error(error.message)}}
    timer=setTimeout(refresh,2000);
    return () => { disposed = true; clearTimeout(timer); };
  }, [pending]);
  async function act(operation: string) {
    try {
      setBusy(operation);
      const result = await api<any>(operation, { provider: 'codex' });
      setAccounts(current => current.map(account => account.provider === 'codex' ? result : account));
      if (operation === 'accounts.login') message.info('请在系统浏览器中完成官方登录');
    } catch (error: any) { message.error(error.message); }
    finally { setBusy(''); }
  }
  return <><header className="page-header"><div><h1>订阅账号</h1><p>由官方工具管理原生授权，工作台使用独立认证目录。</p></div></header>
    <ConnectionAccess selected={provider || 'codex'} onAPI={onConnections} onAccount={onAccounts} disabled={!!busy} />
    <div className="roles-page"><Alert type="info" showIcon title="登录后保留授权状态，原生任务运行将在隔离适配验证后启用" description="GPT 授权沿用本机路由与 TUN。工作台不读取其他 Codex 认证目录；登录浏览器由官方工具提供，密码和令牌不会经过角色卡片。" />
      <div className="account-grid">{accounts.map(account => <Card key={account.provider} id={`account-${account.provider}`} tabIndex={-1} className={provider === account.provider ? 'account-selected' : undefined} title={account.name} extra={<Tag color={account.loggedIn ? 'green' : 'default'}>{account.loggedIn ? '已登录' : account.loginPending ? '等待浏览器授权' : account.checked ? '未登录' : '尚未检测'}</Tag>}>
        <dl className="account-readiness" aria-label={`${account.name}接入状态`}>
          <dt>官方工具</dt><dd><Tag color={account.installed ? 'green' : 'default'}>{account.installed ? '已找到' : '未找到'}</Tag></dd>
          <dt>账号授权</dt><dd><Tag color={account.loggedIn ? 'green' : account.loginPending ? 'blue' : 'default'}>{account.loggedIn ? '已授权' : account.loginPending ? '等待授权' : account.checked ? '未授权' : '尚未检测'}</Tag></dd>
          <dt>编码执行</dt><dd><Tag color={account.executionReady ? 'green' : 'gold'}>{account.executionReady ? '已就绪' : '尚未就绪'}</Tag></dd>
        </dl>
        {account.provider === 'codex' && <><p>凭证存储：要求系统 keyring；不使用明文文件回退。</p>{account.planType && <p>服务报告的套餐：{account.planType}</p>}</>}
        <Alert type={account.provider === 'claude' ? 'warning' : 'info'} title={account.reason} />
        {account.provider === 'codex' ? <Space wrap style={{ marginTop: 20 }}>
          <Button disabled={!account.installed || !!busy} onClick={() => act('accounts.check')}>检测登录状态</Button>
          <Button type="primary" disabled={!account.loginReady || account.loginPending || !!busy} loading={busy === 'accounts.login'} onClick={() => act('accounts.login')}>{account.loggedIn ? '重新登录 / 切换账号' : '登录 ChatGPT'}</Button>
          {account.loginPending && <Button disabled={!!busy} onClick={() => act('accounts.cancel')}>取消登录</Button>}
          {account.loggedIn && <Popconfirm title="退出工作台管理的 GPT 账号？" description="注销工作台专用认证目录中的授权。" okText="退出" cancelText="取消" onConfirm={() => act('accounts.logout')}><Button disabled={!!busy}>退出账号</Button></Popconfirm>}
        </Space> : <Button disabled style={{ marginTop: 20 }}>等待强制出口验证后开放登录</Button>}
      </Card>)}</div>
      <p className="role-template-note">此处的订阅账号尚不能绑定为开发执行连接。公司中转、Coding Plan 与 DeepSeek 等 API 请在“模型连接”配置。</p>
    </div>
  </>;
}

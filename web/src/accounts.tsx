import { useEffect, useState } from 'react';
import { App, Button, Popconfirm } from 'antd';
import { api } from './api';
import { ConnectionAccess, type AccountProvider } from './connection-guide';
import NativeEnvironment from './native-environment';
import { InfoTip, Page, Row, Status, Toolbar, type Tone } from './ui';

const coordinationLabels: Record<string, string> = { unverified: '等待授权核对', idle: '空闲', executing: '执行占用中', authenticating: '认证操作中', blocked: '清理或交接待核对', closed: '服务已关闭' };

function accountStatus(account: any): [Tone, string] {
  if (account.loggedIn) return ['green', '已登录'];
  if (account.loginPending) return ['blue', '等待浏览器授权'];
  return ['gray', account.checked ? '未登录' : '尚未检测'];
}

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
    let timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const result = await api<any>('accounts.check', { provider: 'codex' });
        if (!disposed) {
          setAccounts(current => current.map(account => account.provider === 'codex' ? result : account));
          if (result.loginPending) timer = setTimeout(refresh, 2000);
        }
      } catch (error: any) { if (!disposed) message.error(error.message); }
    }
    timer = setTimeout(refresh, 2000);
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

  return <>
    <Toolbar title="订阅账号"
      center={<ConnectionAccess selected={provider || 'codex'} onAPI={onConnections} onAccount={onAccounts} disabled={!!busy} />} />
    <Page wide>
      <section className="group">
        <div className="group-header">
          <h2>官方账号</h2>
          <div className="group-extra"><InfoTip title="登录后保留授权状态，原生任务运行将在隔离适配验证后启用">GPT 授权沿用本机路由与 TUN。工作台不读取其他 Codex 认证目录；登录浏览器由官方工具提供，密码和令牌不会经过角色卡片。</InfoTip></div>
        </div>
        <div className="account-grid">{accounts.map(account => {
          const [tone, text] = accountStatus(account);
          return <article key={account.provider} id={`account-${account.provider}`} tabIndex={-1}
            className={`account-card${provider === account.provider ? ' is-focused' : ''}`} aria-label={`${account.name}账号`}>
            <header className="account-card-header">
              <span className={`account-avatar avatar-${account.provider}`} aria-hidden="true">{account.provider === 'codex' ? 'G' : 'C'}</span>
              <h3>{account.name}</h3>
              <Status tone={tone}>{text}</Status>
            </header>
            <div className="group-card compact" aria-label={`${account.name}接入状态`}>
              <Row label="官方工具"><Status tone={account.installed ? 'green' : 'gray'}>{account.installed ? '已找到' : '未找到'}</Status></Row>
              <Row label="账号授权"><Status tone={account.loggedIn ? 'green' : account.loginPending ? 'blue' : 'gray'}>{account.loggedIn ? '已授权' : account.loginPending ? '等待授权' : account.checked ? '未授权' : '尚未检测'}</Status></Row>
              <Row label="编码执行"><Status tone={account.executionReady ? 'green' : 'orange'}>{account.executionReady ? '已就绪' : '尚未就绪'}</Status></Row>
              {account.coordination && <Row label="账号占用">{coordinationLabels[account.coordination.state] || '等待核对'} · {account.coordination.active}/{account.coordination.limit}</Row>}
              {account.provider === 'codex' && <Row label="凭证存储">要求系统 keyring；不使用明文文件回退</Row>}
              {account.planType && <Row label="服务报告的套餐">{account.planType}</Row>}
            </div>
            <p className={`account-reason${account.provider === 'claude' ? ' is-warning' : ''}`}>{account.reason}</p>
            <div className="account-actions">
              {account.provider === 'codex' ? <>
                <Button disabled={!account.installed || !!busy} onClick={() => act('accounts.check')}>检测登录状态</Button>
                <Button type="primary" disabled={!account.loginReady || account.loginPending || !!busy} loading={busy === 'accounts.login'} onClick={() => act('accounts.login')}>{account.loggedIn ? '重新登录 / 切换账号' : '登录 ChatGPT'}</Button>
                {account.loginPending && <Button disabled={!!busy} onClick={() => act('accounts.cancel')}>取消登录</Button>}
                {account.loggedIn && <Popconfirm title="退出工作台管理的 GPT 账号？" description="先停止关联执行并确认清理，再注销工作台专用授权。" okText="退出" cancelText="取消" onConfirm={() => act('accounts.logout')}><Button disabled={!!busy}>退出账号</Button></Popconfirm>}
              </> : <Button disabled>等待强制出口验证后开放登录</Button>}
            </div>
          </article>;
        })}</div>
        <div className="group-footer">此处的订阅账号尚不能绑定为开发执行连接。公司中转、Coding Plan 与 DeepSeek 等 API 请在“模型连接”配置。</div>
      </section>
      <NativeEnvironment provider={provider} />
    </Page>
  </>;
}

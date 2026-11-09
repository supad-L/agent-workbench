import { useEffect, useState, type ReactNode } from 'react';
import { App, Button, Form, Input, Modal, Popover, Space } from 'antd';
import { ApiOutlined, FolderOutlined, PlusOutlined, PieChartOutlined, SettingOutlined, TeamOutlined, UserOutlined } from '@ant-design/icons';
import { api, idempotency, type Project } from './api';
import ProjectView from './project-view';
import Connections from './connections';
import Roles from './roles';
import Usage from './usage';
import Accounts from './accounts';
import type { AccountProvider } from './connection-guide';
import { AppearanceControl } from './appearance';
import { EmptyState, Toolbar } from './ui';
import mark from '../../assets/mark.svg';

type Route = 'projects' | 'connections' | 'roles' | 'usage' | 'accounts';

function NavItem({ active, icon, children, onClick }: { active: boolean; icon: ReactNode; children: ReactNode; onClick: () => void }) {
  return <button type="button" className={active ? 'nav-item selected' : 'nav-item'} aria-current={active ? 'page' : undefined} onClick={onClick}>
    <span className="nav-icon" aria-hidden="true">{icon}</span>
    <span className="nav-text">{children}</span>
  </button>;
}

export default function Workbench() {
  const { message } = App.useApp();
  const [route, setRoute] = useState<Route>('projects');
  const [accountProvider, setAccountProvider] = useState<AccountProvider>();
  const [projects, setProjects] = useState<Project[]>([]);
  const [selected, setSelected] = useState('');
  const [meta, setMeta] = useState<any>();
  const [creating, setCreating] = useState(false);
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm();

  function openAccounts(provider: AccountProvider) { setAccountProvider(provider); setRoute('accounts'); }
  const reload = async () => {
    try { setProjects(await api('projects.list')); } catch (e: any) { message.error(e.message); }
  };
  useEffect(() => {
    void reload();
    api('meta').then(setMeta).catch(e => message.error(e.message));
  }, []);

  async function create() {
    try {
      const values = await form.validateFields();
      setBusy(true);
      const result = await api<Project>('project.create', { name: values.name, source: values.source || '', idempotency_key: idempotency() });
      await reload();
      setSelected(result.id); setRoute('projects'); setCreating(false); form.resetFields();
      message.success('项目已创建，副本已保存在管理区');
    } catch (e: any) { if (e.message) message.error(e.message); }
    finally { setBusy(false); }
  }

  let content: ReactNode;
  if (route === 'connections') content = <Connections onAccounts={openAccounts} onRoles={() => setRoute('roles')} />;
  else if (route === 'roles') content = <Roles />;
  else if (route === 'usage') content = <Usage />;
  else if (route === 'accounts') content = <Accounts provider={accountProvider} onConnections={() => setRoute('connections')} onAccounts={openAccounts} />;
  else if (selected) content = <ProjectView id={selected} onChange={reload} />;
  else content = <>
    <Toolbar title="Agent Workbench" />
    <div className="welcome">
      <img src={mark} alt="" className="welcome-mark" />
      <h1>让方案成为开发的共同起点</h1>
      <p>导入项目与资料，记录需求，确认可以追溯的方案版本。</p>
      <Button type="primary" size="large" icon={<PlusOutlined aria-hidden="true" />} onClick={() => setCreating(true)}>创建第一个项目</Button>
      <small>当前支持手工需求与方案编辑、确认锁定、变更草案和事件恢复。</small>
      {projects.length > 0 && <small>也可以从左侧选择一个项目继续。</small>}
    </div>
  </>;

  return <div className="app-shell">
    <aside className="sidebar">
      <div className="sidebar-top drag-region" />
      <nav className="sidebar-scroll" aria-label="工作台导航">
        <div className="sidebar-section">
          <span>项目</span>
          <Button type="text" size="small" className="sidebar-add" icon={<PlusOutlined aria-hidden="true" />} aria-label="新建项目" onClick={() => setCreating(true)} />
        </div>
        {projects.length === 0 && <button type="button" className="nav-item nav-placeholder" onClick={() => setCreating(true)}>
          <span className="nav-icon" aria-hidden="true"><PlusOutlined /></span><span className="nav-text">新建 / 导入项目</span>
        </button>}
        {projects.map(p => <button type="button" key={p.id} title={p.name}
          className={route === 'projects' && p.id === selected ? 'nav-item selected' : 'nav-item'}
          aria-current={route === 'projects' && p.id === selected ? 'page' : undefined}
          onClick={() => { setSelected(p.id); setRoute('projects'); }}>
          <span className="nav-icon" aria-hidden="true"><FolderOutlined /></span>
          <span className="nav-text">{p.name}</span>
        </button>)}

        <div className="sidebar-section"><span>配置</span></div>
        <NavItem active={route === 'connections'} icon={<ApiOutlined />} onClick={() => setRoute('connections')}>模型连接</NavItem>
        <NavItem active={route === 'roles'} icon={<TeamOutlined />} onClick={() => setRoute('roles')}>Agent 角色</NavItem>
        <NavItem active={route === 'accounts'} icon={<UserOutlined />} onClick={() => setRoute('accounts')}>订阅账号</NavItem>

        <div className="sidebar-section"><span>监控</span></div>
        <NavItem active={route === 'usage'} icon={<PieChartOutlined />} onClick={() => setRoute('usage')}>用量与预算</NavItem>
      </nav>
      <div className="sidebar-footer">
        <Popover trigger="click" placement="topLeft" arrow={false} content={<div className="settings-pop"><AppearanceControl /></div>}>
          <Button type="text" size="small" className="sidebar-settings" icon={<SettingOutlined aria-hidden="true" />}>设置</Button>
        </Popover>
        <span className="sidebar-version" title="真实 Agent 调度将在后续阶段接入">v{meta?.version || '0.1.0'} · M2 开发中</span>
      </div>
    </aside>
    <main className="main-area">{content}</main>
    <Modal title="新建或导入项目" open={creating} onCancel={() => !busy && setCreating(false)} onOk={create} confirmLoading={busy}
      okText="创建项目" cancelText="取消" destroyOnHidden width={480}>
      <Form form={form} layout="vertical" className="sheet-form">
        <Form.Item name="name" label="项目名称" rules={[{ required: true, message: '请填写项目名称' }]}>
          <Input maxLength={160} autoFocus placeholder="例如：内部任务管理系统" />
        </Form.Item>
        <Form.Item label="已有项目目录（可选）" extra="导入时创建独立副本，包含未提交源码；密钥、依赖和生成目录会列入排除清单。">
          <Space.Compact style={{ width: '100%' }}>
            <Form.Item name="source" noStyle><Input placeholder="留空则创建新的管理区仓库" /></Form.Item>
            <Button onClick={async () => { const path = await api<string | null>('choose.directory'); if (path) form.setFieldValue('source', path); }}>选择…</Button>
          </Space.Compact>
        </Form.Item>
      </Form>
    </Modal>
  </div>;
}

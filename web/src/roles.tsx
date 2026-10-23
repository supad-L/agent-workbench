import { useEffect, useState } from 'react';
import { App, Alert, Avatar, Button, Card, Checkbox, Collapse, Drawer, Empty, Form, Input, InputNumber, Popconfirm, Select, Space, Switch, Tag } from 'antd';
import { DeleteOutlined, EditOutlined, PlusOutlined, TeamOutlined } from '@ant-design/icons';
import { api } from './api';
import RoleTrial from './role-trial';

type Choice = { connection_id: string; model_id: string };
type Role = {
  id: string; revision: number; name: string; kind: string; description: string;
  system_prompt: string; primary: Choice; tools: string[]; enabled: boolean;
  fallback: { enabled: boolean; candidates: Choice[]; data_scope: string; max_extra_cost_micros: number; currency?: string };
};
const kinds = [
  { value: 'coordinator', label: '主 Agent' }, { value: 'frontend', label: '前端' },
  { value: 'backend', label: '后端' }, { value: 'tester', label: '测试' },
  { value: 'reviewer', label: '审查' }, { value: 'custom', label: '自定义' },
];
const labels: Record<string, string> = {
  read_project: '读取项目信息', read_plan: '读取确认方案', list_roles: '查看角色',
  read_file: '读取任务文件', write_file: '写入任务文件', apply_patch: '提交范围内补丁',
  run_command: '运行受限命令', draft_plan: '编写方案草案', propose_change: '提出方案变更',
  create_task: '创建任务', dispatch_task: '派发任务', read_artifact: '读取成果',
  read_verification: '读取验证证据', submit_review: '提交审查意见', ask_user: '提出待确认问题',
  record_suggestion: '记录非阻塞建议', get_status: '查看运行状态',
};

export default function Roles() {
  const { message } = App.useApp();
  const [roles, setRoles] = useState<Role[]>([]);
  const [connections, setConnections] = useState<any[]>([]);
  const [templates, setTemplates] = useState<Role[]>([]);
  const [tools, setTools] = useState<string[]>([]);
  const [editing, setEditing] = useState<Role>();
  const [open, setOpen] = useState(false);
  const [trial,setTrial]=useState<Role>();
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm();
  const selectedConnection = Form.useWatch('connection_id', form);
  const fallbackEnabled = Form.useWatch('fallback_enabled', form);
  const reload = async () => {
    const [saved, available, definitions] = await Promise.all([
      api<Role[]>('roles.list'), api<any[]>('connections.list'),
      api<{ templates: Role[]; tools: string[] }>('roles.templates'),
    ]);
    setRoles(saved); setConnections(available); setTemplates(definitions.templates); setTools(definitions.tools);
  };
  useEffect(() => { reload().catch(error => message.error(error.message)); }, []);

  function edit(role?: Role, fromTemplate = false) {
    const value = role || { name: '', kind: 'custom', description: '', system_prompt: '', primary: { connection_id: '', model_id: '' }, tools: [], enabled: false,
      fallback: { enabled: false, candidates: [], data_scope: 'conversation_only', max_extra_cost_micros: 0, currency: 'CNY' } };
    setEditing(fromTemplate ? undefined : role);
    form.setFieldsValue({ ...value, connection_id: value.primary.connection_id || undefined,
      model_id: value.primary.model_id || undefined, enabled: fromTemplate ? false : value.enabled,
      fallback_enabled: value.fallback.enabled, candidates: value.fallback.candidates.map(choice => JSON.stringify(choice)),
      data_scope: value.fallback.data_scope || 'conversation_only', currency: value.fallback.currency || 'CNY',
      extra_budget: value.fallback.max_extra_cost_micros / 1_000_000 });
    setOpen(true);
  }
  async function save() {
    try {
      await form.validateFields();const values=form.getFieldsValue(true); setBusy(true);
      const role: Role = { id: editing?.id || crypto.randomUUID(), revision: editing?.revision || 0,
        name: values.name.trim(), kind: values.kind, description: values.description || '', system_prompt: values.system_prompt,
        primary: { connection_id: values.connection_id || '', model_id: values.model_id || '' },
        tools: values.tools || [], enabled: !!values.enabled,
        fallback: { enabled: !!values.fallback_enabled, candidates: (values.candidates || []).map((choice: string) => JSON.parse(choice)),
          data_scope: values.data_scope, currency: values.currency, max_extra_cost_micros: Math.round((values.extra_budget || 0) * 1_000_000) } };
      await api('roles.save', { role }); await reload(); setOpen(false); message.success('角色已保存');
    } catch (error: any) { if (error.message) message.error(error.message); }
    finally { setBusy(false); }
  }
  async function remove(role: Role) {
    try { await api('roles.delete', { id: role.id, revision: role.revision }); await reload(); message.success('角色已删除'); }
    catch (error: any) { message.error(error.message); }
  }
  const bound = (role: Role) => connections.find(connection => connection.id === role.primary.connection_id);
  const modelOptions = (connections.find(connection => connection.id === selectedConnection)?.models || []).map((model: any) => ({ value: model.id, label: model.id }));
  const candidates = connections.filter(connection => connection.enabled !== false).flatMap(connection =>
    (connection.models || []).map((model: any) => ({ value: JSON.stringify({ connection_id: connection.id, model_id: model.id }), label: `${connection.label} / ${model.id}` })));

  return <>
    <header className="page-header"><div><h1>Agent 角色</h1><p>定义职责和工作方式，为主 Agent 派工准备可复用的角色。</p></div>
      <Button type="primary" icon={<PlusOutlined aria-hidden="true" />} onClick={() => edit()}>新增角色</Button></header>
    <div className="roles-page">
      <Alert showIcon type="info" title="角色规则与任务权限共同生效" description="角色卡片不会扩大任务的文件范围，也不能修改已确认方案。真实派工、代码执行和审查闭环将在后续阶段接通。" />
      <div className="section-heading roles-heading"><h2>已配置角色 <span className="role-count">{roles.length}</span></h2></div>
      {roles.length === 0 ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="从下方模板开始，或新建自己的角色" /> :
        <div className="role-grid">{roles.map(role => {
          const connection = bound(role);
          const model=connection?.models?.find((item:any)=>item.id===role.primary.model_id);
          return <Card key={role.id} className={`role-card role-${role.kind}`}>
            <div className="role-card-heading"><Avatar className="role-avatar" icon={<TeamOutlined aria-hidden="true" />} /><div><h3>{role.name}</h3><Tag>{kinds.find(kind => kind.value === role.kind)?.label}</Tag></div>
              <Tag color={role.enabled ? 'green' : 'default'}>{role.enabled ? '已启用' : '已停用'}</Tag></div>
            <p className="role-description">{role.description || '尚未填写职责说明'}</p>
            <dl className="role-binding"><dt>连接</dt><dd>{connection?.label || '未绑定'}</dd><dt>模型</dt><dd>{role.primary.model_id || '未选择'}</dd></dl>
            <div className="role-tool-list">{role.tools.slice(0, 4).map(tool => <Tag key={tool}>{labels[tool] || tool}</Tag>)}{role.tools.length > 4 && <Tag>另 {role.tools.length - 4} 项</Tag>}</div>
            {role.enabled && connection?.enabled === false && <Alert type="warning" title="绑定连接已停用，角色暂不可运行" />}
            {role.enabled&&connection&&!model&&<Alert type="warning" title="绑定模型已移除，请重新选择模型"/>}
            <div className="role-card-footer"><small>{role.fallback.enabled ? '已配置备用策略' : '自动切换关闭'} · 修订 {role.revision}</small><Space>
              <Button disabled={!role.enabled||!connection||connection.enabled===false||!model} onClick={()=>setTrial(role)}>试运行</Button>
              <Button icon={<EditOutlined aria-hidden="true" />} onClick={() => edit(role)}>编辑</Button>
              <Popconfirm title={`删除角色“${role.name}”？`} description="已有运行记录会保留。" onConfirm={() => remove(role)} okText="删除" cancelText="取消"><Button aria-label={`删除${role.name}`} icon={<DeleteOutlined aria-hidden="true" />} /></Popconfirm>
            </Space></div>
          </Card>;
        })}</div>}
      <div className="section-heading roles-heading"><div><h2>内置角色模板</h2><p className="role-template-note">模板默认停用，复制后可自行调整提示词、模型和工具。</p></div></div>
      <div className="template-grid">{templates.map(template => <button key={template.id} className="role-template" onClick={() => edit(template, true)}><strong>{template.name}</strong><span>{template.description}</span><small>从模板新建</small></button>)}</div>
    </div>
    <Drawer title={editing ? '编辑角色' : '新增角色'} open={open} onClose={() => !busy && setOpen(false)} size="large"
      extra={<Button type="primary" loading={busy} onClick={save}>保存角色</Button>}>
      <Form form={form} layout="vertical" onValuesChange={changed => { if ('connection_id' in changed) form.setFieldValue('model_id', undefined); }}>
        <div className="two-col"><Form.Item name="name" label="角色名称" rules={[{ required: true, whitespace: true, message: '请填写角色名称' }]}><Input maxLength={128} /></Form.Item>
          <Form.Item name="kind" label="角色类型" rules={[{ required: true }]}><Select options={kinds} /></Form.Item></div>
        <Form.Item name="description" label="职责说明"><Input.TextArea rows={2} maxLength={2048} /></Form.Item>
        <Form.Item name="system_prompt" label="系统角色提示词" rules={[{ required: true, whitespace: true, message: '请填写角色的行为规则' }]}><Input.TextArea rows={7} maxLength={32768} /></Form.Item>
        <div className="two-col"><Form.Item name="connection_id" label="使用的连接"><Select allowClear placeholder="选择已保存的连接" options={connections.map(connection => ({ value: connection.id, label: connection.label, disabled: connection.enabled === false }))} /></Form.Item>
          <Form.Item name="model_id" label="使用的模型" rules={[{ validator: (_, value) => selectedConnection && !value ? Promise.reject(new Error('请选择此连接上的模型')) : Promise.resolve() }]}><Select virtual={false} allowClear placeholder="先选择连接" options={modelOptions} /></Form.Item></div>
        {connections.length === 0 && <Alert type="warning" showIcon title="先在“模型连接”保存一个连接，或先将角色保存为停用。" />}
        <Form.Item name="tools" label="允许使用的工具" extra="任务合同还会进一步限制目录与命令。配置工具不代表当前阶段已实现该工具。"><Checkbox.Group className="role-tool-options" options={tools.map(tool => ({ value: tool, label: labels[tool] || tool }))} /></Form.Item>
        <Form.Item name="enabled" label="启用角色" valuePropName="checked"><Switch checkedChildren="启用" unCheckedChildren="停用" /></Form.Item>
        <Collapse items={[{ key: 'fallback', label: '备用模型与自动切换', children: <>
          <Form.Item name="fallback_enabled" label="允许自动切换" valuePropName="checked"><Switch /></Form.Item>
          {fallbackEnabled && <><Form.Item name="candidates" label="候选连接与模型" rules={[{ required: true, type: 'array', min: 1, max: 4, message: '请选择 1–4 个候选' }]}><Select mode="multiple" options={candidates} /></Form.Item>
            <Form.Item name="data_scope" label="允许向候选连接发送的资料范围"><Select options={[{ value: 'conversation_only', label: '仅本次对话' }, { value: 'project_summary', label: '本次对话与项目/方案摘要' }]} /></Form.Item>
            <div className="two-col"><Form.Item name="currency" label="费用币种"><Select options={[{ value: 'CNY', label: '人民币 CNY' }, { value: 'USD', label: '美元 USD' }]} /></Form.Item>
              <Form.Item name="extra_budget" label="额外费用上限" rules={[{ required: true, type: 'number', min: 0.000001, message: '请设置大于零的上限' }]}><InputNumber min={0} max={1000000} precision={6} style={{ width: '100%' }} /></Form.Item></div>
            <Alert type="info" title="默认不会跨连接发送资料。认证或权限失败不自动换账号；价格未知时不能将费用算作零。" /></>}
        </> }]} />
      </Form>
    </Drawer>
    {trial&&<RoleTrial role={trial} onClose={()=>setTrial(undefined)}/>}
  </>;
}

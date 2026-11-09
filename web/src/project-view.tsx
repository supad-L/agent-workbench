import { useEffect, useRef, useState } from 'react';
import { App, Button, Drawer, Form, Input, Segmented, Select, Spin, Table, Typography } from 'antd';
import { CheckOutlined, EditOutlined, FileAddOutlined, FileTextOutlined, MessageOutlined, PlusOutlined, SafetyCertificateOutlined, SendOutlined } from '@ant-design/icons';
import { api, idempotency, type Bundle, type Doc, type EventRecord, type Plan } from './api';
import NativeSessions from './native-sessions';
import Tasks from './tasks';
import { EmptyState, Group, InfoTip, Notice, Page, Row, Status, Toolbar } from './ui';

const lines = (value: string) => value.split('\n').map(v => v.trim()).filter(Boolean);
type View = 'workspace' | 'tasks' | 'documents' | 'native' | 'events';

export default function ProjectView({ id, onChange }: { id: string; onChange: () => void }) {
  const { message } = App.useApp();
  const [bundle, setBundle] = useState<Bundle>();
  const [busy, setBusy] = useState(false);
  const [text, setText] = useState('');
  const [events, setEvents] = useState<EventRecord[]>([]);
  const [version, setVersion] = useState<number>();
  const [view, setView] = useState<View>('workspace');
  const [editing, setEditing] = useState<'new' | 'change' | 'edit' | null>(null);
  const [planForm] = Form.useForm();
  const [openedDoc, setOpenedDoc] = useState<Doc>();
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const latest = useRef(id);
  latest.current = id;

  async function reload() {
    const result = await api<Bundle>('project.get', { id });
    if (latest.current === id) {
      setBundle(result);
      setVersion(current => result.plans.some(p => p.version === current) ? current : result.plans.at(-1)?.version);
    }
  }
  useEffect(() => {
    setBundle(undefined); setEvents([]); setVersion(undefined); setView('workspace');
    reload().catch(e => message.error(e.message));
    const off = window.workbench.onEvent(event => {
      if (event.project_id !== id) return;
      setEvents(current => current.some(item => item.sequence === event.sequence) ? current : [...current, event].slice(-100));
      if (timer.current) clearTimeout(timer.current);
      timer.current = setTimeout(() => reload().catch(e => message.error(e.message)), 80);
    });
    api('events.start', { id }).catch(e => message.error(e.message));
    return () => { off(); if (timer.current) clearTimeout(timer.current); api('events.stop').catch(() => {}); };
  }, [id]);

  const selected = bundle?.plans.find(p => p.version === version);
  const active = bundle?.plans.find(p => p.version === bundle.project.active_version);

  async function act(fn: () => Promise<void>) {
    setBusy(true);
    try { await fn(); await reload(); onChange(); }
    catch (e: any) { message.error(e.message); }
    finally { setBusy(false); }
  }
  function edit(mode: 'new' | 'change' | 'edit') {
    const source = mode === 'change' ? active : mode === 'edit' ? selected : undefined;
    const p = source?.content;
    planForm.setFieldsValue({
      title: p?.title || '',
      requirements: p?.requirements?.map((r: any) => r.text).join('\n') || '',
      constraints: p?.engineering_constraints?.map((r: any) => r.text).join('\n') || '遵守已确认范围，保持构建与测试有效',
      acceptance: p?.acceptance?.map((r: any) => r.description).join('\n') || '',
      non_goals: p?.non_goals?.join('\n') || '',
      reason: '',
    });
    setEditing(mode);
  }
  async function saveDraft() {
    const values = await planForm.validateFields();
    const requirements = lines(values.requirements).map((value, i) => ({ id: `REQ-${i + 1}`, text: value }));
    const content = {
      schema_version: 1, project_id: id, version: selected?.version || 1, title: values.title, requirements,
      engineering_constraints: lines(values.constraints).map((value, i) => ({ id: `ENG-${i + 1}`, text: value })),
      non_goals: lines(values.non_goals || ''),
      acceptance: lines(values.acceptance).map((value, i) => ({ id: `AC-${i + 1}`, requirement_ids: requirements.map(r => r.id), description: value, verification_profile: 'verify-default' })),
      autonomy: { ordinary_tasks: 'automatic', unresolved_questions: 'ask_user', product_changes: 'user_confirmed' },
    };
    await act(async () => {
      let result: Plan;
      if (editing === 'edit' && selected) {
        result = await api('plan.update', { id, version: selected.version, expected_revision: selected.revision, expected_hash: selected.hash, content, idempotency_key: idempotency() });
      } else {
        result = await api('plan.create', { id, content, base_version: bundle?.project.active_version || 0, reason: values.reason || '', idempotency_key: idempotency() });
      }
      setVersion(result.version); setEditing(null);
      message.success('草案已保存，尚未替换当前确认方案');
    });
  }

  if (!bundle) return <><Toolbar title="项目" /><div className="loading"><Spin /><p>正在读取项目</p></div></>;

  const conversation = <section className="pane pane-conversation" aria-label="需求记录">
    <div className="pane-header">
      <h2>需求记录</h2>
      <InfoTip title="手工记录">当前记录手工输入；真实主 Agent 对话将在 M3 接通。</InfoTip>
    </div>
    <div className="messages">
      {bundle.messages.length === 0
        ? <EmptyState icon={<MessageOutlined />} title="还没有需求记录">记录项目目标、需求边界和待讨论问题</EmptyState>
        : bundle.messages.map(m => <article key={m.id} className={'message ' + (m.role === 'user' ? 'from-user' : 'from-agent')}>
          <div className="message-meta"><b>{m.role === 'user' ? '你' : '合成 / Agent 输入'}</b><time>{new Date(m.created_at).toLocaleString()}</time></div>
          <p>{m.text}</p>
        </article>)}
    </div>
    <div className="composer">
      <Input.TextArea value={text} onChange={e => setText(e.target.value)} autoSize={{ minRows: 2, maxRows: 7 }}
        placeholder="记录需求或补充说明，内容会持久保存" maxLength={32768} variant="borderless" />
      <div className="composer-bar">
        <small>手工记录，保存后不可修改</small>
        <Button type="primary" icon={<SendOutlined aria-hidden="true" />} disabled={!text.trim()} loading={busy}
          onClick={() => act(async () => { await api('message.append', { id, text, idempotency_key: idempotency() }); setText(''); })}>保存消息</Button>
      </div>
    </div>
  </section>;

  const plan = <section className="pane pane-plan" aria-label="方案版本">
    <div className="pane-header">
      <h2>方案版本</h2>
      <div className="pane-actions">
        <Select style={{ width: 136 }} value={version} placeholder="选择版本" onChange={setVersion}
          options={bundle.plans.map(p => ({ value: p.version, label: `V${p.version} · ${p.status === 'confirmed' ? '已确认' : '草案'}` }))} />
        <Button icon={<PlusOutlined aria-hidden="true" />} onClick={() => edit(active ? 'change' : 'new')}>{active ? '提出变更' : '编写草案'}</Button>
      </div>
    </div>
    <div className="pane-scroll">
      {selected ? <article className="plan-doc">
        <div className="plan-status">
          <Status tone={selected.status === 'confirmed' ? 'green' : 'orange'}>V{selected.version} {selected.status === 'confirmed' ? '已确认并锁定' : '待确认草案'}</Status>
          <Typography.Text copyable={{ text: selected.hash }} className="hash">{selected.hash.slice(0, 24)}…</Typography.Text>
        </div>
        <h3 className="plan-title">{selected.content.title}</h3>
        {selected.reason && <Notice tone="blue" title={`基于 V${selected.base_version} 的变更`}>{selected.reason}</Notice>}
        <div className="plan-section">
          <h4>需求</h4>
          {selected.content.requirements.map((r: any) => <p key={r.id}><span className="req-id">{r.id}</span>{r.text}</p>)}
        </div>
        <div className="plan-section">
          <h4>工程约束</h4>
          {selected.content.engineering_constraints.map((r: any) => <p key={r.id}>{r.text}</p>)}
        </div>
        <div className="plan-section">
          <h4>验收条目</h4>
          {selected.content.acceptance.map((r: any) => <p key={r.id} className="check"><CheckOutlined aria-hidden="true" />{r.description}</p>)}
        </div>
        {selected.content.non_goals.length > 0 && <div className="plan-section">
          <h4>本版不做</h4>
          {selected.content.non_goals.map((r: string, i: number) => <p key={i} className="muted">{r}</p>)}
        </div>}
        <div className="plan-actions">
          {selected.status === 'draft' ? <>
            <div className="plan-buttons">
              <Button icon={<EditOutlined aria-hidden="true" />} onClick={() => edit('edit')}>编辑草案</Button>
              <Button type="primary" loading={busy} onClick={() => act(async () => {
                await api('plan.confirm', { id, version: selected.version, expected_revision: selected.revision, expected_hash: selected.hash, idempotency_key: idempotency() });
                message.success(`V${selected.version} 已确认并锁定`);
              })}>确认 V{selected.version}</Button>
            </div>
            <small>确认会锁定此内容；后续修改需要新建变更草案。</small>
          </> : <Notice tone="green" title="此版本内容已锁定，创建新草案不会覆盖它。" />}
        </div>
      </article> : <EmptyState icon={<FileTextOutlined />} title="还没有方案" action={
        <Button onClick={() => act(async () => {
          const content = await api('plan.template');
          const created = await api<Plan>('plan.create', { id, content, reason: '', base_version: 0, idempotency_key: idempotency() });
          setVersion(created.version);
        })}>载入合成示例草案</Button>}>先编写手工草案，确认后成为固定的开发依据</EmptyState>}
    </div>
  </section>;

  const documents = <Page>
    <Group title="项目资料" extra={<Button size="small" icon={<FileAddOutlined aria-hidden="true" />} loading={busy} onClick={() => act(async () => {
      const path = await api<string | null>('choose.document');
      if (path) { await api('document.add', { id, path, idempotency_key: idempotency() }); message.success('资料已导入并保留来源副本'); }
    })}>导入 Markdown / 文本 / PDF</Button>} footer="资料更新不会自动改写已确认方案。">
      {bundle.documents.length === 0
        ? <div className="row-empty">导入需求文档或项目说明</div>
        : bundle.documents.map(doc => <Row key={doc.id} label={<span className="doc-name"><FileTextOutlined aria-hidden="true" /> {doc.name} <span className="badge">V{doc.version}</span></span>} detail={<span className="hash">{doc.hash}</span>}>
          <Status tone={doc.status === 'ready' ? 'green' : 'orange'}>{doc.status === 'ready' ? '文本已提取' : '需要额外解析'}</Status>
          <Button type="link" size="small" onClick={() => setOpenedDoc(doc)}>查看内容</Button>
        </Row>)}
    </Group>
    <Group title="导入副本">
      <Row label="原始目录"><span className="value-text">{bundle.project.source || '新建项目'}</span></Row>
      <Row label="管理区目录"><Typography.Text copyable className="value-text mono">{bundle.project.snapshot}</Typography.Text></Row>
      <Row label="导入记录">{`${bundle.project.manifest.length} 项，排除 ${bundle.project.manifest.filter(e => e.excluded).length} 项`}</Row>
    </Group>
    <Group title="导入清单">
      <Table className="flush-table" size="small" rowKey="path" pagination={{ pageSize: 10, size: 'small', hideOnSinglePage: true }} dataSource={bundle.project.manifest}
        locale={{ emptyText: '没有导入文件' }} columns={[
          { title: '路径', dataIndex: 'path', ellipsis: true, render: value => <span className="mono">{value}</span> },
          { title: '类型', dataIndex: 'kind', width: 90 },
          { title: '导入状态', width: 150, render: (_, entry) => entry.excluded ? <Status tone="orange">{entry.excluded}</Status> : <Status tone="green">已复制</Status> },
        ]} />
    </Group>
  </Page>;

  const eventList = <Page>
    <Group title="事件记录" extra={<InfoTip title="持久事件">事件与数据修改在同一事务中提交；重新打开项目后会从持久记录恢复。</InfoTip>}>
      {events.length === 0 ? <div className="row-empty">打开项目后的事件会显示在这里</div>
        : [...events].reverse().map(event => <Row key={event.sequence} label={<span className="mono">{event.kind}</span>} detail={new Date(event.created_at).toLocaleString()}>
          <span className="badge">#{event.sequence}</span>
        </Row>)}
    </Group>
  </Page>;

  return <>
    <Toolbar title={bundle.project.name}
      subtitle={<Status tone={active ? 'green' : 'gray'}>{active ? `当前确认 V${active.version}` : '尚未确认方案'}</Status>}
      center={<Segmented<View> aria-label="项目视图" value={view} onChange={setView} options={[
        { value: 'workspace', label: '需求与方案' },
        { value: 'tasks', label: '任务' },
        { value: 'documents', label: `资料 ${bundle.documents.length}` },
        { value: 'native', label: '原生会话' },
        { value: 'events', label: '事件' },
      ]} />}
      actions={<Button icon={<SafetyCertificateOutlined aria-hidden="true" />} onClick={() => act(async () => {
        const result = await api('permission.demo', { id });
        if (!result.agent_can_confirm) message.success('权限验证通过：Agent 确认请求被拒绝（403）');
      })}>验证权限边界</Button>} />
    {view === 'workspace' && <div className="split-view">{conversation}{plan}</div>}
    {view === 'tasks' && <Tasks project={bundle.project} />}
    {view === 'documents' && documents}
    {view === 'native' && <Page><NativeSessions projectID={id} /></Page>}
    {view === 'events' && eventList}

    <Drawer title={editing === 'edit' ? '编辑未确认草案' : editing === 'change' ? `基于 V${active?.version} 提出变更` : '编写手工草案'}
      open={editing !== null} onClose={() => !busy && setEditing(null)} size="large"
      extra={<Button type="primary" loading={busy} onClick={() => saveDraft().catch(() => {})}>保存草案</Button>}>
      <Form form={planForm} layout="vertical" className="panel-form">
        <Form.Item name="title" label="方案标题" rules={[{ required: true }]}><Input /></Form.Item>
        <Form.Item name="requirements" label="需求（每行一项）" rules={[{ required: true }]}><Input.TextArea rows={5} /></Form.Item>
        <Form.Item name="constraints" label="工程约束（每行一项）" rules={[{ required: true }]}><Input.TextArea rows={3} /></Form.Item>
        <Form.Item name="acceptance" label="验收条目（每行一项）" rules={[{ required: true }]}><Input.TextArea rows={4} /></Form.Item>
        <Form.Item name="non_goals" label="本版不做（每行一项）"><Input.TextArea rows={3} /></Form.Item>
        {bundle.project.active_version > 0 && editing !== 'edit' && <Form.Item name="reason" label="变更原因" rules={[{ required: true, message: '请说明相对当前方案的变更原因' }]}><Input.TextArea rows={3} /></Form.Item>}
        <p className="form-note">普通开发任务自动执行；无法确认的问题交给用户，新产品要求需确认新版本。</p>
      </Form>
    </Drawer>
    <Drawer title={openedDoc?.name} open={!!openedDoc} onClose={() => setOpenedDoc(undefined)} size="large">
      {openedDoc?.status === 'ready'
        ? <pre className="document-text">{openedDoc.text}</pre>
        : <Notice tone="orange" title="这份资料尚未提取到可用文本。">扫描 PDF 或复杂格式需要额外解析，未将其视为已读内容。</Notice>}
    </Drawer>
  </>;
}

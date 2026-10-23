import { useEffect, useState } from 'react';
import { App, Alert, Button, Drawer, Form, Input, Radio, Select, Space, Tag } from 'antd';
import { api, idempotency, Project } from './api';

const states: Record<string, string> = { running: '运行中', completed: '已完成试运行', failed: '未完成', cancelled: '已取消', interrupted: '已中断，可手动继续' };
export default function RoleTrial({ role, onClose }: { role: any; onClose: () => void }) {
  const { message } = App.useApp();
  const [projects, setProjects] = useState<Project[]>([]);
  const [runs, setRuns] = useState<any[]>([]);
  const [selected, setSelected] = useState<any>();
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm();
  const project = Form.useWatch('project_id', form);
  useEffect(() => { api<Project[]>('projects.list').then(setProjects).catch(error => message.error(error.message)); }, []);
  useEffect(() => {
    let disposed = false;
    if (project) api<any[]>('runs.list', { id: project }).then(result => { if (!disposed) setRuns(current=>[...current,...result.filter(run => run.role.id === role.id&&!current.some(existing=>existing.id===run.id))]); }).catch(error => message.error(error.message));
    return () => { disposed = true; };
  }, [project, role.id]);
  useEffect(() => {
    if (!selected || selected.status !== 'running' || !project) return;
    let disposed = false, timer: ReturnType<typeof setTimeout>;
    async function refresh() {
      try {
        const result = await api<any>('runs.get', { id: project, run_id: selected.id });
        if (disposed) return;
        setSelected(result);
        setRuns(current=>current.map(run=>run.id===result.id?result:run));
        if (result.status === 'running') timer = setTimeout(refresh, 800);
      } catch (error: any) { if (!disposed) message.error(error.message); }
    }
    timer = setTimeout(refresh, 300);
    return () => { disposed = true; clearTimeout(timer); };
  }, [selected?.id, selected?.status, project]);
  async function start() {
    if(busy)return;
    setBusy(true);
    try {
      const values = await form.validateFields();
      const run = await api<any>('runs.start', { id: values.project_id, role_id: role.id, role_revision: role.revision,
        input: values.input, scope: values.scope, idempotency_key: idempotency() });
      setSelected(run); setRuns(current => [run, ...current]);
    } catch (error: any) { if (error.message) message.error(error.message); }
    finally { setBusy(false); }
  }
  async function operate(operation: string) {
    try {
      setBusy(true);
      const result = await api<any>(operation, { id: project, run_id: selected.id });
      if (operation === 'runs.resume') setSelected(result);
      else message.success('已请求取消，正在保存运行与用量记录');
    } catch (error: any) { message.error(error.message); }
    finally { setBusy(false); }
  }
  return <Drawer title={`${role.name} · 只读试运行`} open size="large" onClose={onClose}>
    <Alert type="info" showIcon title="验证角色与模型连接，会消耗 API 额度" description="仅开放授权范围内的只读平台信息，不写项目文件或运行命令。关闭面板不会取消后台运行。" />
    <Form form={form} layout="vertical" initialValues={{ scope: 'conversation_only' }} style={{ marginTop: 22 }}>
      <Form.Item name="project_id" label="关联项目" rules={[{ required: true, message: '请选择项目' }]}><Select virtual={false} onChange={()=>{setSelected(undefined);setRuns([])}} disabled={selected?.status === 'running'||busy} options={projects.map(item => ({ value: item.id, label: item.name }))} /></Form.Item>
      <Form.Item name="input" label="试运行输入" rules={[{ required: true, whitespace: true, message: '请填写输入' }]}><Input.TextArea disabled={selected?.status==='running'} rows={4} maxLength={16384} /></Form.Item>
      <Form.Item name="scope" label="允许发送的资料范围"><Radio.Group disabled={selected?.status==='running'} options={[{ value: 'conversation_only', label: '角色提示词与本次对话' }, { value: 'project_summary', label: '另允许读取项目与确认方案摘要' }]} /></Form.Item>
      <Button type="primary" onClick={start} loading={busy} disabled={selected?.status === 'running'}>开始试运行（消耗 API 额度）</Button>
    </Form>
    {runs.length > 0 && <div style={{ marginTop: 24 }}><Select aria-label="历史试运行" style={{ width: '100%' }} placeholder="查看本角色的历史试运行"
      value={selected?.id} options={runs.map(run => ({ value: run.id, label: `${new Date(run.created_at).toLocaleString()} · ${states[run.status] || run.status}` }))}
      onChange={id => api<any>('runs.get', { id: project, run_id: id }).then(setSelected).catch(error => message.error(error.message))} /></div>}
    {selected && <section className="trial-result"><Space><Tag color={selected.status === 'completed' ? 'green' : selected.status === 'running' ? 'blue' : 'orange'}>{states[selected.status] || selected.status}</Tag><small>已发起 {selected.state.attempts} 次请求</small></Space>
      {selected.error && <Alert type="warning" showIcon title={selected.error} style={{ marginTop: 14 }} />}
      <Input.TextArea readOnly aria-label="试运行输出" rows={10} style={{ marginTop: 18 }} value={selected.state.completed ? selected.state.text : selected.preview || ''} placeholder="等待模型输出。未完成的文本不作为交付成果。" />
      <Space style={{ marginTop: 16 }}>{selected.status === 'running' && <Button danger loading={busy} onClick={() => operate('runs.cancel')}>取消运行</Button>}
        {['interrupted', 'cancelled', 'failed'].includes(selected.status) && selected.state.attempts < selected.max_turns * (selected.retries + 1) && <Button loading={busy} onClick={() => operate('runs.resume')}>继续运行（可能再次计费）</Button>}</Space>
    </section>}
  </Drawer>;
}

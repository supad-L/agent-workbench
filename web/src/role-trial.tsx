import { useEffect, useRef, useState } from 'react';
import { App, Alert, Button, Collapse, Drawer, Form, Input, Radio, Select, Space } from 'antd';
import { api, idempotency, type EventRecord, type Project } from './api';
import RunDetails, { runStates } from './run-details';

export default function RoleTrial({ role, onClose }: { role: any; onClose: () => void }) {
  const { message } = App.useApp();
  const [projects, setProjects] = useState<Project[]>([]);
  const [runs, setRuns] = useState<any[]>([]);
  const [selected, setSelected] = useState<any>();
  const [historyID, setHistoryID] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [historyBusy, setHistoryBusy] = useState(false);
  const [composerOpen, setComposerOpen] = useState(true);
  const [trace, setTrace] = useState<{ key: string; events: EventRecord[]; error: string }>({ key: '', events: [], error: '' });
  const [traceRevision, setTraceRevision] = useState(0);
  const [snapshotError, setSnapshotError] = useState('');
  const [snapshotRevision, setSnapshotRevision] = useState(0);
  const selection = useRef(0);
  const [form] = Form.useForm();
  const project = Form.useWatch('project_id', form);

  useEffect(() => {
    let disposed = false;
    api<Project[]>('projects.list').then(value => { if (!disposed) setProjects(value); }).catch(error => { if (!disposed) message.error(error.message); });
    return () => { disposed = true; selection.current += 1; };
  }, []);
  useEffect(() => {
    let disposed = false;
    if (project) api<any[]>('runs.list', { id: project }).then(result => {
      if (!disposed) setRuns(current => [...current, ...result.filter(run => run.role.id === role.id && !current.some(existing => existing.id === run.id))]);
    }).catch(error => { if (!disposed) message.error(error.message); });
    return () => { disposed = true; };
  }, [project, role.id]);

  useEffect(() => {
    const key = `${project}/${selected?.id}`;
    setTrace({ key, events: [], error: '' });
    if (!selected?.id || !project) return;
    let disposed = false;
    const runID = selected.id;
    const off = window.workbench.onEvent(event => {
      const payload = event.payload as { run_id?: string } | null;
      if (disposed || event.project_id !== project || !payload || payload.run_id !== runID || !['run.started', 'run.progress', 'run.finished'].includes(event.kind)) return;
      setTrace(current => current.key !== key || current.events.some(item => item.sequence === event.sequence) ? current : { ...current, events: [...current.events, event].sort((a, b) => a.sequence - b.sequence).slice(-200) });
    });
    api('events.start', { id: project }).catch(error => { if (!disposed) setTrace(current => current.key === key ? { ...current, error: error.message } : current); });
    return () => { disposed = true; off(); api('events.stop').catch(() => {}); };
  }, [selected?.id, project, traceRevision]);

  useEffect(() => {
    setSnapshotError('');
    if (!selected || selected.status !== 'running' || !project) return;
    let disposed = false, timer: ReturnType<typeof setTimeout>;
    const runID = selected.id;
    async function refresh() {
      try {
        const result = await api<any>('runs.get', { id: project, run_id: runID });
        if (disposed) return;
        setSelected(result);
        setRuns(current => current.map(run => run.id === result.id ? result : run));
        if (result.status === 'running') timer = setTimeout(refresh, 800);
      } catch (error: any) { if (!disposed) setSnapshotError(error.message); }
    }
    timer = setTimeout(refresh, 300);
    return () => { disposed = true; clearTimeout(timer); };
  }, [selected?.id, selected?.status, project, snapshotRevision]);

  function changeProject() {
    selection.current += 1;
    setSelected(undefined); setHistoryID(undefined); setRuns([]); setHistoryBusy(false); setSnapshotError('');
  }
  async function chooseRun(id: string) {
    const generation = ++selection.current;
    setHistoryID(id); setSelected(undefined); setHistoryBusy(true); setSnapshotError('');
    try {
      const run = await api<any>('runs.get', { id: project, run_id: id });
      if (generation !== selection.current) return;
      setSelected(run); setComposerOpen(false);
      setRuns(current => current.map(item => item.id === run.id ? run : item));
    } catch (error: any) { if (generation === selection.current) setSnapshotError(error.message); }
    finally { if (generation === selection.current) setHistoryBusy(false); }
  }
  async function start() {
    if (busy || historyBusy) return;
    const generation = ++selection.current;
    setBusy(true);
    try {
      const values = await form.validateFields();
      const run = await api<any>('runs.start', { id: values.project_id, role_id: role.id, role_revision: role.revision,
        input: values.input, scope: values.scope, idempotency_key: idempotency() });
      if (generation !== selection.current) return;
      setSelected(run); setHistoryID(run.id); setComposerOpen(false);
      setRuns(current => [run, ...current.filter(item => item.id !== run.id)]);
    } catch (error: any) { if (generation === selection.current && error.message) message.error(error.message); }
    finally { setBusy(false); }
  }
  async function operate(operation: string) {
    if (busy || !selected) return;
    const generation = selection.current;
    setBusy(true);
    try {
      const result = await api<any>(operation, { id: project, run_id: selected.id });
      if (generation !== selection.current) return;
      if (operation === 'runs.resume') {
        setSelected(result);
        setRuns(current => current.map(run => run.id === result.id ? result : run));
      } else message.success('已请求取消，正在保存运行与用量记录');
    } catch (error: any) { if (generation === selection.current) message.error(error.message); }
    finally { setBusy(false); }
  }
  const running = selected?.status === 'running';
  const traceMatches = trace.key === `${project}/${selected?.id}`;
  const events = traceMatches ? trace.events : [];
  const traceError = traceMatches ? trace.error : '';
  return <Drawer title={`${role.name} · 只读试运行`} open size="large" onClose={onClose}>
    <Alert type="info" showIcon title="验证角色与模型连接，会消耗 API 额度" description="仅开放授权范围内的只读平台信息，不写项目文件或运行命令。关闭面板不会取消后台运行。" />
    <Collapse className="trial-composer" activeKey={composerOpen ? ['input'] : []} onChange={keys => setComposerOpen(keys.includes('input'))} items={[
      { key: 'input', label: selected ? '填写新的试运行输入' : '选择项目与输入', forceRender: true, children: <Form form={form} layout="vertical" initialValues={{ scope: 'conversation_only' }}>
        <Form.Item name="project_id" label="关联项目" rules={[{ required: true, message: '请选择项目' }]}><Select virtual={false} onChange={changeProject} disabled={running || busy} options={projects.map(item => ({ value: item.id, label: item.name }))} /></Form.Item>
        <Form.Item name="input" label="试运行输入" rules={[{ required: true, whitespace: true, message: '请填写输入' }]}><Input.TextArea disabled={running || busy} rows={4} maxLength={16384} /></Form.Item>
        <Form.Item name="scope" label="允许发送的资料范围"><Radio.Group disabled={running || busy} options={[{ value: 'conversation_only', label: '角色提示词与本次对话' }, { value: 'project_summary', label: '另允许读取项目与确认方案摘要' }]} /></Form.Item>
        <Button type="primary" onClick={start} loading={busy} disabled={running || historyBusy}>开始试运行（消耗 API 额度）</Button>
      </Form> },
    ]} />
    {runs.length > 0 && <div className="trial-history"><label htmlFor="trial-history">历史试运行</label><Select id="trial-history" aria-label="历史试运行" virtual={false} style={{ width: '100%' }} placeholder="查看本角色的历史试运行"
      loading={historyBusy} disabled={busy || running} value={historyID}
      options={runs.map(run => ({ value: run.id, label: `${new Date(run.created_at).toLocaleString()} · ${runStates[run.status] || run.status}` }))} onChange={chooseRun} /></div>}
    {snapshotError && <Alert type="warning" showIcon title="运行记录暂不可用" description={snapshotError}
      action={<Button size="small" onClick={() => selected ? setSnapshotRevision(value => value + 1) : historyID && chooseRun(historyID)}>重试读取记录</Button>} />}
    {selected && <>
      <RunDetails key={selected.id} run={selected} events={events} traceError={traceError} onRetryTrace={() => setTraceRevision(value => value + 1)} />
      <Space className="trial-controls">{running && <Button danger loading={busy} onClick={() => operate('runs.cancel')}>取消运行</Button>}
        {['interrupted', 'cancelled', 'failed'].includes(selected.status) && selected.state.attempts < selected.max_turns * (selected.retries + 1) && <Button loading={busy} onClick={() => operate('runs.resume')}>继续运行（可能再次计费）</Button>}</Space>
    </>}
  </Drawer>;
}

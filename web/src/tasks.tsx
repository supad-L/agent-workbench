import { useEffect, useMemo, useRef, useState } from 'react';
import { App, Button, Form, Input, Segmented, Select, Tabs } from 'antd';
import { CodeOutlined, ExportOutlined, PlusOutlined, StopOutlined, RedoOutlined } from '@ant-design/icons';
import { api, idempotency, type EventRecord, type Project } from './api';
import { EmptyState, Notice, Row, Status, type Tone } from './ui';

export type FileDiff = { path: string; kind: string; binary?: boolean; too_large?: boolean; truncated?: boolean; added: number; removed: number; text?: string };
type Task = any;

export const taskStates: Record<string, string> = {
  running: '执行中', completed: '执行结束', failed: '未完成', cancelled: '已取消', interrupted: '已中断，可手动继续',
};
const taskTone = (status: string): Tone => status === 'completed' ? 'green' : status === 'running' ? 'blue' : status === 'cancelled' ? 'gray' : 'orange';
const changeLabels: Record<string, string> = { added: '新增', modified: '修改', deleted: '删除' };
const toolLabels: Record<string, string> = {
  list_files: '列出文件', read_file: '读取文件', write_file: '写入文件', replace_in_file: '替换内容', run_command: '运行验证命令',
};

/** Roles that can execute a task: enabled, bound to a connection, able to read and modify files. */
export function taskEligibility(role: any, connections: any[]): string {
  if (!role.enabled) return '角色已停用';
  const tools: string[] = role.tools || [];
  if (!tools.includes('read_file') || !(tools.includes('write_file') || tools.includes('apply_patch'))) return '需要启用读取与写入任务文件';
  const connection = connections.find(item => item.id === role.primary?.connection_id);
  if (!connection) return '尚未绑定模型连接';
  if (connection.enabled === false) return '绑定连接已停用';
  const model = (connection.models || []).find((item: any) => item.id === role.primary?.model_id);
  if (!model) return '绑定模型已移除';
  if (model.tools === 'unsupported') return '所选模型不支持工具调用';
  return '';
}

function directoriesOf(project?: Project): string[] {
  const dirs = new Set<string>();
  for (const entry of project?.manifest || []) {
    if (entry.excluded) continue;
    const parts = entry.path.split('/');
    const limit = entry.kind === 'directory' ? parts.length : parts.length - 1;
    for (let i = 1; i <= Math.min(limit, 3); i++) dirs.add(parts.slice(0, i).join('/') + '/');
  }
  return [...dirs].sort();
}

function eventLabel(event: EventRecord): string {
  const payload = event.payload as Record<string, any>;
  if (event.kind === 'run.started') return '开始执行任务';
  if (event.kind === 'run.finished') return `执行结束：${taskStates[String(payload.status)] || '状态已更新'}`;
  const tool = toolLabels[payload.tool] || payload.tool || '工具';
  switch (payload.type) {
    case 'tool_started': return `${tool}…`;
    case 'tool_finished': return `${tool}完成`;
    case 'retry_wait': return '等待有限重试';
    case 'turn_finished': return '模型回合结束';
    case 'text_delta': return '模型输出已更新';
    default: return '运行进度已更新';
  }
}

/** Unified diff with old/new line numbers; headers are summarized above. */
export function DiffView({ text }: { text: string }) {
  const rows = useMemo(() => {
    const result: { kind: string; old?: number; next?: number; text: string }[] = [];
    let oldLine = 0, newLine = 0;
    for (const line of text.split('\n')) {
      if (!line || line.startsWith('diff --git') || line.startsWith('--- ') || line.startsWith('+++ ') || line.startsWith('new file mode') || line.startsWith('deleted file mode')) continue;
      const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
      if (hunk) { oldLine = Number(hunk[1]); newLine = Number(hunk[2]); result.push({ kind: 'hunk', text: line }); continue; }
      if (line.startsWith('\\')) { result.push({ kind: 'note', text: '（文件末尾没有换行）' }); continue; }
      const marker = line[0];
      if (marker === '+') result.push({ kind: 'add', next: newLine++, text: line.slice(1) });
      else if (marker === '-') result.push({ kind: 'del', old: oldLine++, text: line.slice(1) });
      else result.push({ kind: 'ctx', old: oldLine++, next: newLine++, text: line.slice(1) });
    }
    return result;
  }, [text]);
  return <div className="diff-view" role="table" aria-label="文件差异">
    {rows.map((row, i) => <div key={i} className={`diff-row diff-${row.kind}`} role="row">
      <span className="diff-num" aria-hidden="true">{row.old ?? ''}</span>
      <span className="diff-num" aria-hidden="true">{row.next ?? ''}</span>
      <span className="diff-mark" aria-hidden="true">{row.kind === 'add' ? '+' : row.kind === 'del' ? '−' : ''}</span>
      <code className="diff-text">{row.text}</code>
    </div>)}
  </div>;
}

function NewTaskForm({ project, roles, connections, onStarted, onCancel, onRoles }: {
  project?: Project; roles: any[]; connections: any[]; onStarted: (task: Task) => void; onCancel?: () => void; onRoles?: () => void;
}) {
  const { message } = App.useApp();
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const runtime = Form.useWatch('runtime', form);
  const roleID = Form.useWatch('role_id', form);
  const role = roles.find(item => item.id === roleID);
  const connection = connections.find(item => item.id === role?.primary?.connection_id);
  const directories = useMemo(() => directoriesOf(project), [project]);
  const usable = roles.filter(item => !taskEligibility(item, connections));
  async function start() {
    if (busy) return;
    try {
      const values = await form.validateFields();
      const chosen = roles.find(item => item.id === values.role_id);
      if (!chosen) return;
      setBusy(true);
      const task = await api<Task>('tasks.start', {
        id: project?.id, role_id: chosen.id, role_revision: chosen.revision, title: values.title, instructions: values.instructions,
        write_dirs: values.write_dirs, runtime: values.runtime === 'none' ? '' : values.runtime, image: values.runtime === 'none' ? '' : (values.image || '').trim(),
        idempotency_key: idempotency(),
      });
      form.resetFields();
      onStarted(task);
    } catch (error: any) { if (error?.message) message.error(error.message); }
    finally { setBusy(false); }
  }
  return <div className="detail-inner">
    <div className="detail-heading"><h2>新建开发任务</h2></div>
    {usable.length === 0 && <Notice tone="orange" title="还没有可以执行任务的角色" action={onRoles && <Button size="small" onClick={onRoles}>前往配置角色</Button>}>
      需要一个已启用、绑定了 API 连接，并允许“读取任务文件”和“写入任务文件”的角色。
    </Notice>}
    <Form form={form} layout="horizontal" colon={false} className="mac-form" initialValues={{ runtime: 'none', write_dirs: [] }}>
      <div className="form-group">
        <div className="form-group-title">任务</div>
        <div className="group-card">
          <Form.Item name="title" label="任务标题" rules={[{ required: true, whitespace: true, message: '请填写任务标题' }]}><Input maxLength={160} placeholder="例如：登录页增加“记住我”选项" /></Form.Item>
          <Form.Item name="instructions" label="任务说明" className="stack" rules={[{ required: true, whitespace: true, message: '请说明要完成什么' }]} extra="写清楚目标、验收方式和不要改动的地方。已确认的方案会一并提供给模型。">
            <Input.TextArea rows={6} maxLength={16000} placeholder="要实现什么、改哪些行为、如何算完成" />
          </Form.Item>
        </div>
      </div>
      <div className="form-group">
        <div className="form-group-title">执行</div>
        <div className="group-card">
          <Form.Item name="role_id" label="执行角色" rules={[{ required: true, message: '请选择执行角色' }]}>
            <Select placeholder="选择角色" virtual={false} options={roles.map(item => {
              const reason = taskEligibility(item, connections);
              return { value: item.id, disabled: !!reason, label: reason ? `${item.name}（${reason}）` : item.name };
            })} />
          </Form.Item>
          <Form.Item name="write_dirs" label="允许修改的目录" rules={[{ required: true, type: 'array', min: 1, max: 8, message: '请选择 1–8 个目录' }]} extra="只有这些目录里的文件可以被创建或修改；可以输入新目录名。">
            <Select mode="tags" virtual={false} placeholder="例如 src/ 或 web/src/" tokenSeparators={[',', ' ']} options={directories.map(dir => ({ value: dir, label: dir }))} />
          </Form.Item>
        </div>
      </div>
      <div className="form-group">
        <div className="form-group-title">验证命令（可选）</div>
        <div className="group-card">
          <Form.Item name="runtime" label="容器运行时">
            <Segmented options={[{ value: 'none', label: '不运行命令' }, { value: 'podman', label: 'Podman' }, { value: 'docker', label: 'Docker' }]} />
          </Form.Item>
          {runtime && runtime !== 'none' ? <Form.Item name="image" label="本地镜像 ID" rules={[{ required: true, whitespace: true, message: '请填写本地镜像的完整 ID' }]} extra="命令在断网容器里只读运行项目副本，用于测试或构建检查，不能安装依赖。角色需允许“运行受限命令”。">
            <Input className="mono-input" placeholder="sha256:…（可在“订阅账号 → 执行环境检测”中确认）" autoComplete="off" />
          </Form.Item> : <div className="row-note">不提供命令工具，模型只能读写文件。</div>}
        </div>
      </div>
    </Form>
    <Notice tone="blue" title="项目文件会发送给模型服务">
      {connection ? `执行时，模型读取的文件内容会发送给“${connection.label}”（${role?.primary?.model_id}），按 API 用量计费。` : '执行时，模型读取的文件内容会发送给角色绑定的模型服务，按 API 用量计费。'}
      改动只写入任务副本，不会修改原项目；任务不切换备用连接。
    </Notice>
    <div className="form-actions">
      {onCancel && <Button onClick={onCancel}>取消</Button>}
      <Button type="primary" loading={busy} disabled={usable.length === 0} onClick={() => void start()}>开始执行（消耗 API 额度）</Button>
    </div>
  </div>;
}

function TaskDetail({ projectID, task, events, onChanged }: { projectID: string; task: Task; events: EventRecord[]; onChanged: (task: Task) => void }) {
  const { message } = App.useApp();
  const [files, setFiles] = useState<FileDiff[]>();
  const [selectedFile, setSelectedFile] = useState('');
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const generation = useRef(0);
  const running = task.status === 'running';
  async function loadChanges() {
    const current = ++generation.current;
    try {
      const result = await api<{ files: FileDiff[] }>('tasks.changes', { id: projectID, run_id: task.id });
      if (current !== generation.current) return;
      setFiles(result.files); setError('');
      setSelectedFile(path => result.files.some(file => file.path === path) ? path : result.files[0]?.path || '');
    } catch (e: any) { if (current === generation.current) setError(e.message); }
  }
  useEffect(() => { setFiles(undefined); setSelectedFile(''); void loadChanges(); return () => { generation.current++; }; }, [task.id]);
  useEffect(() => { if (!running) void loadChanges(); }, [task.status]);
  useEffect(() => {
    if (!running) return;
    const timer = setInterval(() => void loadChanges(), 3000);
    return () => clearInterval(timer);
  }, [running, task.id]);
  async function operate(operation: string) {
    if (busy) return;
    setBusy(operation);
    try {
      if (operation === 'tasks.exportPatch') {
        const result = await api<{ saved: string | null; files?: number }>(operation, { id: projectID, run_id: task.id, name: task.task?.title });
        if (result.saved) message.success(`已导出 ${result.files} 个文件的改动`);
      } else {
        const result = await api<any>(operation, { id: projectID, run_id: task.id });
        if (operation === 'runs.resume') onChanged(result);
        else message.success('已请求取消，正在保存运行与用量记录');
      }
    } catch (e: any) { message.error(e.message); }
    finally { setBusy(''); }
  }
  const choice = task.choices?.[task.choice_index || 0];
  const file = files?.find(item => item.path === selectedFile);
  const resultText = task.state?.completed ? task.state.text : task.preview || '';
  const tools = Object.entries(task.state?.tool_results || {}) as [string, any][];
  const changeCount = files?.length ?? task.task?.changes?.length ?? 0;
  return <div className="detail-inner wide" aria-label="任务详情">
    <div className="task-heading">
      <div>
        <h2>{task.task?.title || '开发任务'}</h2>
        <div className="task-meta"><Status tone={taskTone(task.status)}>{taskStates[task.status] || task.status}</Status><span>{new Date(task.created_at).toLocaleString()}</span></div>
      </div>
      <div className="task-actions">
        {running && <Button danger icon={<StopOutlined aria-hidden="true" />} loading={busy === 'runs.cancel'} onClick={() => void operate('runs.cancel')}>取消执行</Button>}
        {['interrupted', 'cancelled', 'failed'].includes(task.status) && task.state.attempts < task.max_turns * (task.retries + 1) &&
          <Button icon={<RedoOutlined aria-hidden="true" />} loading={busy === 'runs.resume'} onClick={() => void operate('runs.resume')}>继续执行（可能再次计费）</Button>}
        <Button icon={<ExportOutlined aria-hidden="true" />} disabled={!files?.some(item => item.text)} loading={busy === 'tasks.exportPatch'} onClick={() => void operate('tasks.exportPatch')}>导出补丁</Button>
      </div>
    </div>
    {task.error && <Notice tone="orange" title={task.error} />}
    <div className="group-card compact">
      <Row label="执行角色">{task.role?.name}</Row>
      <Row label="模型连接"><span className="value-text">{choice?.connection?.label || '记录未提供'} · <span className="mono">{choice?.model?.id}</span></span></Row>
      <Row label="允许修改">{(task.task?.write_dirs || []).map((dir: string) => <span key={dir} className="chip mono">{dir}</span>)}</Row>
      <Row label="验证命令">{task.task?.image ? <span className="value-text mono">{task.task.runtime} · {task.task.image.slice(0, 19)}…</span> : '未启用'}</Row>
      <Row label="模型请求">{task.state?.attempts || 0} 次 · 最多 {task.max_turns * (task.retries + 1)} 次</Row>
    </div>
    <Tabs className="task-tabs" defaultActiveKey="changes" items={[
      { key: 'changes', label: `改动 (${changeCount})`, children: <>
        <p className="form-note">改动只存在于任务副本中，执行结束不代表已审查或已合并到原项目。</p>
        {error && <Notice tone="orange" title="改动暂不可读" action={<Button size="small" onClick={() => void loadChanges()}>重试</Button>}>{error}</Notice>}
        {files && files.length === 0 && <EmptyState icon={<CodeOutlined />} title={running ? '还没有改动' : '没有改动'}>{running ? '模型修改文件后会显示在这里' : '这次执行没有修改任何文件'}</EmptyState>}
        {files && files.length > 0 && <div className="change-browser">
          <div className="change-list" role="list" aria-label="改动文件">
            {files.map(item => <button type="button" role="listitem" key={item.path} className={item.path === selectedFile ? 'change-item selected' : 'change-item'} onClick={() => setSelectedFile(item.path)}>
              <span className={`change-kind change-${item.kind}`}>{changeLabels[item.kind] || item.kind}</span>
              <span className="change-path mono" title={item.path}>{item.path}</span>
              <span className="change-stat"><span className="add">+{item.added}</span> <span className="del">−{item.removed}</span></span>
            </button>)}
          </div>
          <div className="change-diff">
            {file && (file.binary ? <EmptyState title="二进制文件已变更">不显示二进制内容</EmptyState>
              : file.too_large ? <EmptyState title="文件过大">超过 1 MiB 或差异过于复杂，不显示逐行差异</EmptyState>
              : file.truncated ? <EmptyState title="差异已截断">总差异超过显示上限，请导出补丁查看</EmptyState>
              : file.text ? <DiffView text={file.text} /> : <EmptyState title="仅文件属性变化" />)}
          </div>
        </div>}
      </> },
      { key: 'result', label: '模型说明', children: <>
        {resultText ? <pre className="task-result">{resultText}</pre> : <EmptyState title={running ? '等待模型完成' : '没有模型说明'}>{running ? '执行结束后显示模型的总结' : '这次执行没有产生最终说明'}</EmptyState>}
        <p className="form-note">Token 用量与费用请在“用量与预算”查看；未知用量不按零处理。</p>
      </> },
      { key: 'process', label: `过程 (${tools.length})`, children: <>
        {events.length > 0 && <ol className="trial-timeline">{events.map(event => <li key={event.sequence}>
          <div><time dateTime={event.created_at}>{new Date(event.created_at).toLocaleTimeString()}</time><small>#{event.sequence}</small></div>
          <span>{eventLabel(event)}</span>
        </li>)}</ol>}
        {tools.length === 0 ? <EmptyState title="还没有工具调用" /> : <div className="tool-log">
          {tools.map(([id, result]) => <details key={id} className="tool-log-item">
            <summary><span>{toolLabels[result.name] || result.name}</span><code>{summarizeArguments(result.arguments)}</code></summary>
            <pre>{result.text}</pre>
          </details>)}
        </div>}
      </> },
    ]} />
  </div>;
}

function summarizeArguments(args: any): string {
  if (!args || typeof args !== 'object') return '';
  if (typeof args.command === 'string') return args.command.slice(0, 120);
  if (typeof args.path === 'string') return args.path;
  return '';
}

export default function Tasks({ project, onRoles }: { project: Project; onRoles?: () => void }) {
  const { message } = App.useApp();
  const [tasks, setTasks] = useState<Task[]>([]);
  const [roles, setRoles] = useState<any[]>([]);
  const [connections, setConnections] = useState<any[]>([]);
  const [selected, setSelected] = useState<string>();
  const [creating, setCreating] = useState(false);
  const [events, setEvents] = useState<EventRecord[]>([]);
  const projectID = project.id;
  const listGeneration = useRef(0);
  async function reload() {
    const current = ++listGeneration.current;
    const result = await api<Task[]>('tasks.list', { id: projectID });
    if (current !== listGeneration.current) return;
    setTasks(result);
    setSelected(id => id && result.some(task => task.id === id) ? id : result[0]?.id);
    if (result.length === 0) setCreating(true);
  }
  useEffect(() => {
    setTasks([]); setSelected(undefined); setEvents([]); setCreating(false);
    reload().catch(error => message.error(error.message));
    Promise.all([api<any[]>('roles.list'), api<any[]>('connections.list')]).then(([r, c]) => { setRoles(r); setConnections(c); }).catch(error => message.error(error.message));
    return () => { listGeneration.current++; };
  }, [projectID]);
  // The project view owns the event stream; this view only listens for its task runs.
  useEffect(() => window.workbench.onEvent(event => {
    if (event.project_id !== projectID) return;
    const payload = event.payload as { run_id?: string } | null;
    if (!payload?.run_id || !['run.started', 'run.progress', 'run.finished'].includes(event.kind)) return;
    setEvents(current => current.some(item => item.sequence === event.sequence) ? current : [...current, event].slice(-400));
    if (event.kind !== 'run.progress' || (event.payload as any).type === 'tool_finished') reload().catch(() => {});
  }), [projectID]);
  const anyRunning = tasks.some(task => task.status === 'running');
  useEffect(() => {
    if (!anyRunning) return;
    const timer = setInterval(() => reload().catch(() => {}), 1500);
    return () => clearInterval(timer);
  }, [anyRunning, projectID]);
  const current = tasks.find(task => task.id === selected);
  const currentEvents = events.filter(event => (event.payload as any)?.run_id === current?.id);
  return <div className="master-detail">
    <aside className="list-pane" aria-label="开发任务">
      <div className="list-pane-header"><h2>开发任务</h2><span className="count">{tasks.length}</span>
        <Button size="small" type="text" className="list-pane-add" icon={<PlusOutlined aria-hidden="true" />} aria-label="新建任务" onClick={() => setCreating(true)} />
      </div>
      <div className="list-pane-scroll">
        {tasks.length === 0 ? <EmptyState icon={<CodeOutlined />} title="还没有任务">在右侧描述第一个开发任务</EmptyState>
          : tasks.map(task => <button type="button" key={task.id} className={!creating && task.id === selected ? 'list-item selected' : 'list-item'} onClick={() => { setSelected(task.id); setCreating(false); }}>
            <span className="list-icon" aria-hidden="true"><CodeOutlined /></span>
            <span className="list-text">
              <strong>{task.task?.title || '开发任务'}</strong>
              <small>{task.role?.name} · {(task.task?.changes || []).length} 个文件改动</small>
              <small>{new Date(task.created_at).toLocaleString()}</small>
            </span>
            <Status tone={taskTone(task.status)}>{taskStates[task.status] || task.status}</Status>
          </button>)}
      </div>
      <div className="list-pane-footer">任务在项目副本中执行，原项目不会被修改。</div>
    </aside>
    <section className="detail-pane">
      <div className="detail-scroll">
        {creating || !current
          ? <NewTaskForm project={project} roles={roles} connections={connections} onRoles={onRoles} onCancel={tasks.length ? () => setCreating(false) : undefined}
            onStarted={task => { setTasks(list => [task, ...list.filter(item => item.id !== task.id)]); setSelected(task.id); setCreating(false); }} />
          : <TaskDetail key={current.id} projectID={projectID} task={current} events={currentEvents} onChanged={task => setTasks(list => list.map(item => item.id === task.id ? task : item))} />}
      </div>
    </section>
  </div>;
}

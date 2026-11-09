import { Button, Input, Tabs } from 'antd';
import { type EventRecord } from './api';
import { EmptyState, Notice, Row, Status, type Tone } from './ui';

export const runStates: Record<string, string> = {
  running: '运行中', completed: '已完成试运行', failed: '未完成',
  cancelled: '已取消', interrupted: '已中断，可手动继续',
};
const tools: Record<string, string> = {
  read_project: '读取项目信息', read_plan: '读取确认方案',
  list_roles: '查看角色', get_status: '查看运行状态',
};
export function runEventLabel(event: EventRecord): string {
  const payload = event.payload as Record<string, unknown>;
  if (event.kind === 'run.started') return '开始试运行';
  if (event.kind === 'run.finished') return `试运行结束：${runStates[String(payload.status)] || '状态已更新'}`;
  const tool = typeof payload.tool === 'string' ? tools[payload.tool] || payload.tool : '只读工具';
  switch (payload.type) {
    case 'text_delta': return '模型输出已更新';
    case 'tool_started': return `调用工具：${tool}`;
    case 'tool_finished': return `工具已返回：${tool}`;
    case 'retry_wait': return '等待有限重试';
    case 'turn_finished': return '模型回合结束';
    default: return '运行进度已更新';
  }
}

const runTone = (status: string): Tone => status === 'completed' ? 'green' : status === 'running' ? 'blue' : 'orange';

export default function RunDetails({ run, events, traceError, onRetryTrace }: {
  run: any; events: EventRecord[]; traceError: string; onRetryTrace: () => void;
}) {
  const choice = run.choices?.[run.choice_index || 0];
  const recorded = Object.entries(run.state.tool_results || {}) as [string, { name: string }][];
  const pending = run.state.pending || [];
  const limit = run.max_turns ? run.max_turns * (run.retries + 1) : undefined;
  return <section className="trial-result" aria-label="本次试运行详情">
    <div className="trial-run-heading">
      <div><h2>{run.role.name || '角色试运行'}</h2><small className="mono">运行编号 {run.id}</small></div>
      <Status tone={runTone(run.status)}>{runStates[run.status] || run.status}</Status>
    </div>
    <dl className="stat-row">
      <div className="stat"><dt>请求尝试</dt><dd>{run.state.attempts}{limit ? <small> / 最多 {limit} 次</small> : <small> 次</small>}</dd></div>
      <div className="stat"><dt>已处理模型回合</dt><dd>{run.state.next_turn || 0}</dd></div>
      <div className="stat"><dt>已记录工具结果</dt><dd>{recorded.length}</dd></div>
    </dl>
    {run.error && <Notice tone="orange" title={run.error} />}
    <Tabs defaultActiveKey="output" items={[
      { key: 'output', label: '输出与快照', children: <>
        <div className="group-card compact">
          <Row label="本次连接">{choice?.connection?.label || '记录未提供'}</Row>
          <Row label="本次模型"><span className="mono">{choice?.model?.id || run.role.primary?.model_id || '记录未提供'}</span></Row>
          <Row label="资料范围">{run.scope === 'project_summary' ? '对话与项目 / 确认方案摘要' : '角色提示词与本次对话'}</Row>
          <Row label="开始时间">{new Date(run.created_at).toLocaleString()}</Row>
          <Row label="最近保存">{run.updated_at ? new Date(run.updated_at).toLocaleString() : '记录未提供'}</Row>
        </div>
        <InputOutput run={run} />
      </> },
      { key: 'events', label: `运行事件 (${events.length})`, children: <>
        <p className="form-note">显示本次运行最近 200 条持久事件；历史回放只更新展示，不会重新请求模型。</p>
        {traceError && <Notice tone="orange" title="运行事件暂不可用"
          action={<Button size="small" onClick={onRetryTrace}>重新订阅事件</Button>}>{traceError}</Notice>}
        {events.length === 0 ? <EmptyState title="尚未收到本次运行的事件">输出与状态仍可查看</EmptyState> :
          <ol className="trial-timeline">{events.map(event => <li key={event.sequence}>
            <div><time dateTime={event.created_at}>{new Date(event.created_at).toLocaleTimeString()}</time><small>#{event.sequence}</small></div>
            <span>{runEventLabel(event)}</span>
          </li>)}</ol>}
      </> },
      { key: 'tools', label: `只读工具 (${recorded.length})`, children: <>
        <p className="form-note">工具返回来自保存的检查点；工具记录不代表代码开发或项目验收完成。</p>
        {recorded.length === 0 && pending.length === 0 ? <EmptyState title="本次运行尚无工具记录" /> :
          <div className="group-card compact">
            {recorded.map(([id, result]) => <Row key={id} label={tools[result.name] || result.name} detail={<span className="mono">{id}</span>}><Status tone="green">返回已记录</Status></Row>)}
            {pending.map((call: any) => <Row key={call.id} label={tools[call.name] || call.name} detail={<span className="mono">{call.id}</span>}><Status tone="gray">等待处理</Status></Row>)}
          </div>}
      </> },
    ]} />
  </section>;
}

// Output is draft text until the saved state confirms a successful model turn.
function InputOutput({ run }: { run: any }) {
  return <>
    {run.input && <details className="trial-input"><summary>查看本次输入</summary><p>{run.input}</p></details>}
    <Input.TextArea readOnly aria-label="试运行输出" rows={9} className="output-text"
      value={run.state.completed ? run.state.text : run.preview || ''}
      placeholder="等待模型输出。未完成的文本不作为交付成果。" />
    <p className="form-note">Token 用量和费用请在“用量与预算”查看；未知用量不按零处理。</p>
  </>;
}

import { Alert, Button, Descriptions, Empty, Input, Tabs, Tag } from 'antd';
import { type EventRecord } from './api';

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

export default function RunDetails({ run, events, traceError, onRetryTrace }: {
  run: any; events: EventRecord[]; traceError: string; onRetryTrace: () => void;
}) {
  const choice = run.choices?.[run.choice_index || 0];
  const recorded = Object.entries(run.state.tool_results || {}) as [string, { name: string }][];
  const pending = run.state.pending || [];
  const limit = run.max_turns ? run.max_turns * (run.retries + 1) : undefined;
  return <section className="trial-result" aria-label="本次试运行详情">
    <div className="trial-run-heading"><div><h2>{run.role.name || '角色试运行'}</h2><small>运行编号 {run.id}</small></div>
      <Tag color={run.status === 'completed' ? 'green' : run.status === 'running' ? 'blue' : 'orange'}>{runStates[run.status] || run.status}</Tag></div>
    <dl className="trial-counters">
      <div><dt>请求尝试</dt><dd>{run.state.attempts}{limit ? <small> / 最多 {limit} 次</small> : ' 次'}</dd></div>
      <div><dt>已处理模型回合</dt><dd>{run.state.next_turn || 0}</dd></div>
      <div><dt>已记录工具结果</dt><dd>{recorded.length}</dd></div>
    </dl>
    {run.error && <Alert type="warning" showIcon title={run.error} />}
    <Tabs defaultActiveKey="output" items={[
      { key: 'output', label: '输出与快照', children: <>
        <Descriptions column={1} size="small" items={[
          { key: 'connection', label: '本次连接', children: choice?.connection?.label || '记录未提供' },
          { key: 'model', label: '本次模型', children: choice?.model?.id || run.role.primary?.model_id || '记录未提供' },
          { key: 'scope', label: '资料范围', children: run.scope === 'project_summary' ? '对话与项目 / 确认方案摘要' : '角色提示词与本次对话' },
          { key: 'created', label: '开始时间', children: new Date(run.created_at).toLocaleString() },
          { key: 'updated', label: '最近保存', children: run.updated_at ? new Date(run.updated_at).toLocaleString() : '记录未提供' },
        ]} />
        <InputOutput run={run} />
      </> },
      { key: 'events', label: `运行事件 (${events.length})`, children: <>
        <p className="trial-detail-note">显示本次运行最近 200 条持久事件；历史回放只更新展示，不会重新请求模型。</p>
        {traceError && <Alert type="warning" showIcon title="运行事件暂不可用" description={traceError}
          action={<Button size="small" onClick={onRetryTrace}>重新订阅事件</Button>} />}
        {events.length === 0 ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="尚未收到本次运行的事件，输出与状态仍可查看" /> :
          <ol className="trial-timeline">{events.map(event => <li key={event.sequence}>
            <div><time dateTime={event.created_at}>{new Date(event.created_at).toLocaleTimeString()}</time><small>#{event.sequence}</small></div>
            <span>{runEventLabel(event)}</span>
          </li>)}</ol>}
      </> },
      { key: 'tools', label: `只读工具 (${recorded.length})`, children: <>
        <p className="trial-detail-note">工具返回来自保存的检查点；工具记录不代表代码开发或项目验收完成。</p>
        {recorded.length === 0 && pending.length === 0 ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="本次运行尚无工具记录" /> :
          <ul className="trial-tool-records">{recorded.map(([id, result]) => <li key={id}><div><strong>{tools[result.name] || result.name}</strong><small>{id}</small></div><Tag color="green">返回已记录</Tag></li>)}
            {pending.map((call: any) => <li key={call.id}><div><strong>{tools[call.name] || call.name}</strong><small>{call.id}</small></div><Tag>等待处理</Tag></li>)}
          </ul>}
      </> },
    ]} />
  </section>;
}

// Output is draft text until the saved state confirms a successful model turn.
function InputOutput({ run }: { run: any }) {
  return <>
    {run.input && <details className="trial-input"><summary>查看本次输入</summary><p>{run.input}</p></details>}
    <Input.TextArea readOnly aria-label="试运行输出" rows={9}
      value={run.state.completed ? run.state.text : run.preview || ''}
      placeholder="等待模型输出。未完成的文本不作为交付成果。" />
    <p className="trial-detail-note">Token 用量和费用请在“用量与预算”查看；未知用量不按零处理。</p>
  </>;
}

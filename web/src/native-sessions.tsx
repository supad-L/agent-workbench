import { useEffect, useRef, useState } from 'react';
import { App, Button, Drawer, Input, Table, Typography } from 'antd';
import { api } from './api';
import { EmptyState, Group, InfoTip, Notice, Row, Status, type Tone } from './ui';

type Session = {
  id: string; status: string; recovery: string; native_session_id?: string; run_directory: string;
  binding: { model: string; runtime: string }; attempt: number; revision: number; updated_at: string;
  cleanup_verified: boolean; checkpoint: { text?: string; provider_completed: boolean; usage?: { known: boolean; input_tokens: number; output_tokens: number } };
};
type View = { session: Session; can_resume: boolean; recovery_reason: string; live_ready: boolean };
type Detail = { view: View; attempts: { number: number; mode: string; status: string; checkpoint_sequence: number; checkpoint: Session['checkpoint'] }[] };
const statuses: Record<string, string> = { ready: '尚未启动', running: '运行中', completed: '上次回合完成', interrupted: '已中断', failed: '未完成', uncertain: '结果未知', blocked: '未就绪' };
const statusTone = (status: string): Tone => status === 'uncertain' ? 'orange' : status === 'completed' ? 'green' : status === 'running' ? 'blue' : 'gray';
const recoveryLabel = (view: View) => view.can_resume ? '本地续接条件齐备' : view.session.recovery === 'uncertain' ? '结果待核对' : view.session.recovery === 'not_started' ? '未启动过模型请求' : '续接条件不齐';

export default function NativeSessions({ projectID }: { projectID: string }) {
  const { message } = App.useApp();
  const [listing, setListing] = useState<{ projectID: string; items: View[] }>({ projectID, items: [] });
  const [detailSnapshot, setDetailSnapshot] = useState<{ projectID: string; data: Detail }>();
  const views = listing.projectID === projectID ? listing.items : [];
  const detail = detailSnapshot?.projectID === projectID ? detailSnapshot.data : undefined;
  const setViews = (items: View[]) => setListing({ projectID, items });
  const setDetail = (data: Detail | undefined) => setDetailSnapshot(data ? { projectID, data } : undefined);
  const [loading, setLoading] = useState(false);
  const [reading, setReading] = useState(false);
  const [error, setError] = useState('');
  const requests = useRef(0);
  const selections = useRef(0);
  async function reload() {
    const generation = ++requests.current;
    setLoading(true); setError('');
    try { const result = await api<View[]>('native.sessions.list', { id: projectID }); if (requests.current === generation) setViews(result); }
    catch (error: any) { if (requests.current === generation) setError(error.message); }
    finally { if (requests.current === generation) setLoading(false); }
  }
  useEffect(() => {
    setViews([]); setDetail(undefined); void reload();
    return () => { requests.current++; selections.current++; };
  }, [projectID]);
  async function open(id: string) {
    const generation = ++selections.current;
    setDetail(undefined); setReading(true);
    try { const result = await api<Detail>('native.sessions.get', { id: projectID, session_id: id }); if (selections.current === generation) setDetail(result); }
    catch (error: any) { if (selections.current === generation) message.error(error.message); }
    finally { if (selections.current === generation) setReading(false); }
  }
  const selected = detail?.view.session;
  return <section className="group native-sessions">
    <div className="group-header">
      <h2>原生会话记录</h2>
      <div className="group-extra">
        <InfoTip title="持久记录不会自动重发模型请求">此页用于核对会话编号、检查点和恢复条件。</InfoTip>
        <Button size="small" loading={loading || reading} onClick={() => void reload()}>刷新会话记录</Button>
      </div>
    </div>
    {error && <Notice tone="orange" title={error} />}
    <div className="group-card">
      <Table<View> className="flush-table" size="small" loading={loading} rowKey={view => view.session.id} pagination={{ pageSize: 8, size: 'small', hideOnSinglePage: true }} dataSource={views}
        locale={{ emptyText: <EmptyState title="此项目尚无原生会话记录" /> }} columns={[
          { title: '会话 / 模型', render: (_, view) => <div className="native-session-identity"><strong>{view.session.binding.model}</strong><small className="mono">{view.session.id}</small></div> },
          { title: '运行状态', width: 140, render: (_, view) => <Status tone={statusTone(view.session.status)}>{statuses[view.session.status] || view.session.status}</Status> },
          { title: '恢复条件', width: 170, render: (_, view) => <span>{recoveryLabel(view)}</span> },
          { title: '', width: 110, align: 'right', render: (_, view) => <Button size="small" onClick={() => void open(view.session.id)}>查看检查点</Button> },
        ]} />
    </div>
    <div className="group-footer">当前不开放原生启动或继续操作，真实账号与在线隔离执行仍待验收。</div>
    <Drawer title="原生会话与检查点" open={!!detail} onClose={() => { selections.current++; setDetail(undefined); }} size="large">
      {selected && detail && <>
        <div className="drawer-status">
          <Status tone={statusTone(selected.status)}>{statuses[selected.status] || selected.status}</Status>
          <Status tone={detail.view.can_resume ? 'blue' : 'orange'}>{recoveryLabel(detail.view)}</Status>
        </div>
        <p className="form-note">{detail.view.recovery_reason}</p>
        <Group>
          <Row label="工作台会话"><span className="value-text mono">{selected.id}</span></Row>
          <Row label="原生会话编号"><span className="value-text mono">{selected.native_session_id || '尚未获得'}</span></Row>
          <Row label="固定模型"><span className="mono">{selected.binding.model}</span></Row>
          <Row label="隔离运行时">{selected.binding.runtime}</Row>
          <Row label="独立运行目录"><Typography.Text copyable className="value-text mono">{selected.run_directory}</Typography.Text></Row>
          <Row label="容器停止核验"><Status tone={selected.cleanup_verified ? 'green' : 'orange'}>{selected.cleanup_verified ? '已确认' : '尚未确认'}</Status></Row>
          <Row label="提供方终态"><span className="value-text">{selected.checkpoint.provider_completed ? selected.status === 'completed' && selected.cleanup_verified ? '提供方终态、进程退出和清理均已确认' : '已收到提供方成功终态，进程结果仍待核对' : '未记录完整成功终态'}</span></Row>
          <Row label="最近保存">{new Date(selected.updated_at).toLocaleString()}</Row>
        </Group>
        <Group title="检查点草稿">
          <Input.TextArea readOnly rows={6} variant="borderless" aria-label="原生检查点草稿" value={selected.checkpoint.text || ''} placeholder="尚无可用草稿；草稿不作为已验收成果" />
        </Group>
        <Group title="执行尝试" footer="继续会创建新的执行尝试并使用新的显式输入；结果未知不会仅凭会话编号判为可恢复。">
          <Table className="flush-table" size="small" rowKey="number" pagination={false} dataSource={detail.attempts} columns={[
            { title: '尝试', dataIndex: 'number' }, { title: '方式', render: (_, row) => row.mode === 'resume' ? '显式续接' : '首次启动' },
            { title: '状态', render: (_, row) => statuses[row.status] || row.status }, { title: '检查点数', dataIndex: 'checkpoint_sequence' },
          ]} />
        </Group>
        <Button disabled block>账号与在线隔离执行接通后开放继续</Button>
      </>}
    </Drawer>
  </section>;
}

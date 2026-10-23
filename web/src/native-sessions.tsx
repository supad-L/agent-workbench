import { useEffect, useRef, useState } from 'react';
import { Alert, App, Button, Descriptions, Drawer, Empty, Input, Space, Table, Tag, Typography } from 'antd';
import { api } from './api';

type Session = {
  id: string; status: string; recovery: string; native_session_id?: string; run_directory: string;
  binding: { model: string; runtime: string }; attempt: number; revision: number; updated_at: string;
  cleanup_verified: boolean; checkpoint: { text?: string; provider_completed: boolean; usage?: { known: boolean; input_tokens: number; output_tokens: number } };
};
type View = { session: Session; can_resume: boolean; recovery_reason: string; live_ready: boolean };
type Detail = { view: View; attempts: { number: number; mode: string; status: string; checkpoint_sequence: number; checkpoint: Session['checkpoint'] }[] };
const statuses: Record<string, string> = { ready: '尚未启动', running: '运行中', completed: '上次回合完成', interrupted: '已中断', failed: '未完成', uncertain: '结果未知', blocked: '未就绪' };
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
  return <section className="native-sessions">
    <div className="section-heading"><h2>原生会话记录</h2><Button loading={loading || reading} onClick={() => void reload()}>刷新会话记录</Button></div>
    <Alert showIcon type="info" title="持久记录不会自动重发模型请求" description="此页用于核对会话编号、检查点和恢复条件。当前不开放原生启动或继续操作，真实账号与在线隔离执行仍待验收。" />
    {error && <Alert type="warning" showIcon title={error} />}
    <Table<View> className="native-session-table" loading={loading} rowKey={view => view.session.id} pagination={{ pageSize: 8 }} dataSource={views}
      locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="此项目尚无原生会话记录" /> }} columns={[
        { title: '会话 / 模型', render: (_, view) => <div className="native-session-identity"><strong>{view.session.binding.model}</strong><small>{view.session.id}</small></div> },
        { title: '运行状态', render: (_, view) => <Tag color={view.session.status === 'uncertain' ? 'orange' : view.session.status === 'completed' ? 'green' : 'default'}>{statuses[view.session.status] || view.session.status}</Tag> },
        { title: '恢复条件', render: (_, view) => <span>{recoveryLabel(view)}</span> },
        { title: '操作', render: (_, view) => <Button size="small" onClick={() => void open(view.session.id)}>查看检查点</Button> },
      ]} />
    <Drawer title="原生会话与检查点" open={!!detail} onClose={() => { selections.current++; setDetail(undefined); }} size="large">
      {selected && detail && <>
        <Space wrap><Tag>{statuses[selected.status] || selected.status}</Tag><Tag color={detail.view.can_resume ? 'blue' : 'orange'}>{recoveryLabel(detail.view)}</Tag></Space>
        <p className="trial-detail-note">{detail.view.recovery_reason}</p>
        <Descriptions size="small" column={1} items={[
          { key: 'id', label: '工作台会话', children: selected.id },
          { key: 'native', label: '原生会话编号', children: selected.native_session_id || '尚未获得' },
          { key: 'model', label: '固定模型', children: selected.binding.model },
          { key: 'runtime', label: '隔离运行时', children: selected.binding.runtime },
          { key: 'directory', label: '独立运行目录', children: <Typography.Text copyable>{selected.run_directory}</Typography.Text> },
          { key: 'cleanup', label: '容器停止核验', children: selected.cleanup_verified ? '已确认' : '尚未确认' },
          { key: 'provider', label: '提供方终态', children: selected.checkpoint.provider_completed ? selected.status === 'completed' && selected.cleanup_verified ? '提供方终态、进程退出和清理均已确认' : '已收到提供方成功终态，进程结果仍待核对' : '未记录完整成功终态' },
          { key: 'updated', label: '最近保存', children: new Date(selected.updated_at).toLocaleString() },
        ]} />
        <h3>检查点草稿</h3><Input.TextArea readOnly rows={6} aria-label="原生检查点草稿" value={selected.checkpoint.text || ''} placeholder="尚无可用草稿；草稿不作为已验收成果" />
        <h3>执行尝试</h3><Table size="small" rowKey="number" pagination={false} dataSource={detail.attempts} columns={[
          { title: '尝试', dataIndex: 'number' }, { title: '方式', render: (_, row) => row.mode === 'resume' ? '显式续接' : '首次启动' },
          { title: '状态', render: (_, row) => statuses[row.status] || row.status }, { title: '检查点数', dataIndex: 'checkpoint_sequence' },
        ]} />
        <p className="trial-detail-note">继续会创建新的执行尝试并使用新的显式输入；结果未知不会仅凭会话编号判为可恢复。</p>
        <Button disabled>账号与在线隔离执行接通后开放继续</Button>
      </>}
    </Drawer>
  </section>;
}

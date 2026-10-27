import { useEffect, useRef, useState } from 'react';
import { Alert, App, Button, Card, Descriptions, Form, Input, Select, Space, Table, Tag } from 'antd';
import { api } from './api';

type Report = { checked_at: string; image_id?: string; version?: string; ip?: string; environment_ready: boolean; execution_ready: boolean; checks: { name: string; status: string; detail: string }[] };
const names: Record<string,string> = { runtime:'容器运行时', image:'本地镜像', cli:'容器内工具', cli_cleanup:'版本检测清理', network:'网络路径', network_cleanup:'网络检测清理', egress_ip:'出口 IP', isolation:'强制出口隔离', cleanup:'清理状态' };
const statuses: Record<string,string> = { passed:'通过', blocked:'未就绪', skipped:'未执行', unverified:'未验证' };

export default function NativeEnvironment({ provider = 'codex' }: { provider?: string }) {
  const [form] = Form.useForm();
  const { message } = App.useApp();
  const [report,setReport] = useState<Report>();
  const [busy,setBusy] = useState(false);
  const mode = Form.useWatch('mode',form);
  const generation = useRef(0);
  const checking = useRef(false);
  useEffect(() => { generation.current++; setReport(undefined); setBusy(false); form.setFieldsValue({ provider, binary:provider === 'claude' ? '/usr/local/bin/claude' : '/usr/local/bin/codex' }); return () => { generation.current++; }; },[provider]);
  async function check() {
    if (checking.current) return;
    checking.current = true;
    let values;
    try { values = await form.validateFields(); } catch { checking.current = false; return; }
    const current = ++generation.current;
    setBusy(true); setReport(undefined);
    try {
      const result = await api<Report>('native.environment.check',{ ...values, target:values.target || '', proxy:values.mode === 'proxy' ? values.proxy || '' : '' });
      if (current === generation.current) setReport(result);
    } catch (error:any) { if (current === generation.current) message.error(error.message); }
    finally { checking.current = false; if (current === generation.current) { setBusy(false); form.setFieldValue('proxy',''); } }
  }
  async function directory() {
    const current = generation.current;
    try { const selected = await api<string|null>('choose.directory'); if (selected && current === generation.current) { form.setFieldValue('scratch',selected); setReport(undefined); } }
    catch (error:any) { if (current === generation.current) message.error(error.message); }
  }
  return <Card title="执行环境与出口检测" style={{ marginTop:24 }}>
    <p className="trial-detail-note">检测只使用所选本地镜像，不启动模型。填写网络目标后会向该地址发送一次检测请求；留空则只做离线检查。检测结果不改变账号绑定。</p>
    <Form form={form} layout="vertical" disabled={busy} initialValues={{ provider, binary:'/usr/local/bin/codex', mode:'system' }} onValuesChange={changed => { generation.current++; setReport(undefined); if (changed.provider) form.setFieldValue('binary',changed.provider === 'claude' ? '/usr/local/bin/claude' : '/usr/local/bin/codex'); }}>
      <Space align="start" wrap style={{ width:'100%' }}>
        <Form.Item name="provider" label="原生工具" rules={[{ required:true }]}><Select style={{ width:180 }} options={[{ value:'codex',label:'GPT / Codex' },{ value:'claude',label:'Claude Code' }]} /></Form.Item>
        <Form.Item name="runtime" label="容器运行时" rules={[{ required:true,message:'请选择运行时' }]}><Select placeholder="明确选择运行时" style={{ width:220 }} options={[{ value:'podman',label:'Podman' },{ value:'docker',label:'Docker' }]} /></Form.Item>
      </Space>
      <Form.Item name="image" label="本地镜像名称或 ID" rules={[{ required:true,message:'请填写已有本地镜像' }]}><Input placeholder="例如 sha256:…；不会自动拉取镜像" /></Form.Item>
      <Form.Item name="binary" label="容器内工具路径" rules={[{ required:true }]}><Input /></Form.Item>
      <Form.Item label="临时检测目录（需要运行时可访问）" required><Space.Compact style={{ width:'100%' }}><Form.Item name="scratch" noStyle rules={[{ required:true,message:'请选择已有的临时检测目录' }]}><Input placeholder="仅在此目录中新建临时夹具，不挂载项目或账号" /></Form.Item><Button onClick={() => void directory()}>选择目录</Button></Space.Compact></Form.Item>
      <Form.Item name="mode" label="检测网络路径"><Select options={[{ value:'system',label:'跟随运行时网络' },{ value:'proxy',label:'指定代理' }]} /></Form.Item>
      {mode === 'proxy' && <Form.Item name="proxy" label="代理 URL（仅本次使用）" rules={[{ required:true,message:'请填写代理 URL' }]}><Input.Password autoComplete="off" placeholder="HTTP、HTTPS 或 SOCKS5 URL，含端口及必要认证" /></Form.Item>}
      <Form.Item name="target" label="网络检测目标（可选）"><Input placeholder="例如 https://api.ipify.org；返回 IP 或含 ip 字段的 JSON 时显示出口 IP" /></Form.Item>
      <Alert showIcon type="info" title="连通性与完整出口隔离分别记录" description="容器经运行时联网，检测不保证宿主 TUN 生效，也不验证登录浏览器、DNS/IPv6/UDP 旁路。Claude 的强制出口条件不足时不会发送网络请求。" />
      <Button type="primary" loading={busy} disabled={busy} onClick={() => void check()} style={{ marginTop:16 }}>检测执行环境</Button>
    </Form>
    {report && <section aria-label="执行环境检测结果" style={{ marginTop:24 }}>
      <Space wrap><Tag color={report.environment_ready ? 'green' : 'gold'}>{report.environment_ready ? '基础诊断通过' : '仍有条件待核对'}</Tag><Tag>原生编码尚未开放</Tag></Space>
      <Descriptions size="small" column={1} style={{ marginTop:12 }} items={[
        { key:'time',label:'检测时间',children:new Date(report.checked_at).toLocaleString() },
        { key:'image',label:'镜像 ID',children:report.image_id || '未解析' },
        { key:'version',label:'工具版本',children:report.version || '未确认' },
        { key:'ip',label:'本次目标报告的 IP',children:report.ip || '未知 / 未检测' },
      ]} />
      <Table rowKey="name" size="small" pagination={false} dataSource={report.checks} columns={[
        { title:'检查项',render:(_,row) => names[row.name] || row.name },
        { title:'结果',render:(_,row) => <Tag color={row.status === 'passed' ? 'green' : row.status === 'blocked' ? 'gold' : 'default'}>{statuses[row.status] || row.status}</Tag> },
        { title:'说明',dataIndex:'detail' },
      ]} />
    </section>}
  </Card>;
}

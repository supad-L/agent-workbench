import { useEffect, useState } from 'react';
import { App, Alert, Button, Form, InputNumber, Modal, Select, Space, Switch, Table, Tag } from 'antd';
import { api } from './api';

type Summary = { currency: string; cost_micros: number; held_micros: number; known_input_tokens: number; known_output_tokens: number; estimated_input_tokens: number; estimated_output_tokens: number; unknown_requests: number; unpriced_requests: number };

export default function Usage() {
  const { message } = App.useApp();
  const [data, setData] = useState<any>();
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm();
  const reload = async () => setData(await api('usage.get'));
  useEffect(() => { reload().catch(error => message.error(error.message)); }, []);
  function choose(currency: string) { const previous = data?.budgets.find((item: any) => item.currency === currency); form.setFieldsValue({ currency, enabled: previous?.enabled || false, amount: (previous?.limit_micros || 0) / 1_000_000 }); }
  async function save() {
    try {
      const values = await form.validateFields(); setBusy(true);
      const previous = data.budgets.find((item: any) => item.currency === values.currency);
      await api('budget.save', { budget: { currency: values.currency, revision: previous?.revision || 0, enabled: values.enabled, limit_micros: Math.round(values.amount * 1_000_000) } });
      await reload(); setEditing(false); message.success('自定义预算已保存');
    } catch (error: any) { if (error.message) message.error(error.message); }
    finally { setBusy(false); }
  }
  const money = (amount: number) => (amount / 1_000_000).toFixed(6);
  return <><header className="page-header"><div><h1>用量与预算</h1><p>区分服务返回的用量、估算值和未知费用，按价格快照记录。</p></div><Space><Button onClick={() => reload().catch(error => message.error(error.message))}>刷新</Button><Button type="primary" onClick={() => { choose('CNY'); setEditing(true); }}>设置自定义预算</Button></Space></header>
    <div className="roles-page"><Alert type="info" showIcon title="自定义预算与服务商余额、订阅额度分别计算" description="价格来自你填写的快照。请求前进行保守预约；失败或未返回完整用量时保留未确认预留。历史未知费用单独列出，不作为已知零费用。" />
      <Table<Summary> style={{ marginTop: 24 }} rowKey="currency" pagination={false} dataSource={data?.summaries || []} columns={[
        { title: '币种', width:100, render: (_, row) => row.currency || '币种未知' },
        { title: '已知费用', width:140, render: (_, row) => row.currency ? money(row.cost_micros) : '未知' },
        { title: '未确认预留估算', width:180, render: (_, row) => row.currency ? row.unpriced_requests>0&&row.held_micros===0?'待确认':money(row.held_micros) : '无法估算' },
        { title: '已报告输入 / 输出 tokens', width:210, render: (_, row) => `${row.known_input_tokens} / ${row.known_output_tokens}` },
        { title: '估算输入 / 输出 tokens', width:210, render: (_, row) => `${row.estimated_input_tokens} / ${row.estimated_output_tokens}` },
        { title: '未知用量请求', width:140, dataIndex: 'unknown_requests' }, { title: '费用未确认请求', width:140, dataIndex: 'unpriced_requests' },
      ]} scroll={{ x: 1120 }} />
      <div className="section-heading roles-heading"><h2>自定义预算</h2></div>
      {(data?.budgets || []).length === 0 ? <p>尚未启用本地预算。你可以先为连接填写价格，再设置对应币种的上限。</p> : data.budgets.map((budget: any) => <p key={budget.currency}><Tag>{budget.currency}</Tag>{money(budget.limit_micros)} <Tag color={budget.enabled ? 'green' : 'default'}>{budget.enabled ? '已启用' : '已停用'}</Tag></p>)}
      <div className="section-heading roles-heading"><h2>服务商额度</h2></div><p>API 服务余额：未知；GPT / Claude 订阅额度：尚未接入。不会将两者合并成 API 可用金额。</p>
    </div>
    <Modal title="设置本地自定义预算" open={editing} onCancel={() => !busy && setEditing(false)} onOk={save} confirmLoading={busy} okText="保存预算" cancelText="取消"><Form form={form} layout="vertical">
      <Form.Item name="currency" label="预算币种"><Select onChange={choose} options={[{ value: 'CNY', label: '人民币 CNY' }, { value: 'USD', label: '美元 USD' }]} /></Form.Item>
      <Form.Item name="enabled" label="启用此币种预算" valuePropName="checked"><Switch /></Form.Item>
      <Form.Item name="amount" label="上限金额" rules={[{ required: true, type: 'number', min: 0, message: '请填写非负金额；启用时需要大于零' }]}><InputNumber min={0} max={1000000} precision={6} style={{ width: '100%' }} /></Form.Item>
      <Alert type="info" title="启用后，价格未知的模型请求会停止在预约阶段。历史未知费用不包含在已知费用合计中。" />
    </Form></Modal>
  </>;
}

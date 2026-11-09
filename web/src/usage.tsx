import { useEffect, useState } from 'react';
import { App, Button, Form, InputNumber, Modal, Select, Switch, Table } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { api } from './api';
import { Group, InfoTip, Page, Row, Status, Toolbar } from './ui';

type Summary = { currency: string; cost_micros: number; held_micros: number; known_input_tokens: number; known_output_tokens: number; estimated_input_tokens: number; estimated_output_tokens: number; unknown_requests: number; unpriced_requests: number };

const money = (amount: number) => (amount / 1_000_000).toFixed(6);
const symbol = (currency: string) => currency === 'CNY' ? '¥' : currency === 'USD' ? '$' : '';
const count = (value: number) => value.toLocaleString();

export default function Usage() {
  const { message } = App.useApp();
  const [data, setData] = useState<any>();
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [form] = Form.useForm();
  const reload = async () => setData(await api('usage.get'));
  useEffect(() => { reload().catch(error => message.error(error.message)); }, []);

  function choose(currency: string) {
    const previous = data?.budgets.find((item: any) => item.currency === currency);
    form.setFieldsValue({ currency, enabled: previous?.enabled || false, amount: (previous?.limit_micros || 0) / 1_000_000 });
  }
  async function save() {
    try {
      const values = await form.validateFields();
      setBusy(true);
      const previous = data.budgets.find((item: any) => item.currency === values.currency);
      await api('budget.save', { budget: { currency: values.currency, revision: previous?.revision || 0, enabled: values.enabled, limit_micros: Math.round(values.amount * 1_000_000) } });
      await reload(); setEditing(false); message.success('自定义预算已保存');
    } catch (error: any) { if (error.message) message.error(error.message); }
    finally { setBusy(false); }
  }

  const summaries: Summary[] = data?.summaries || [];
  const priced = summaries.filter(row => row.currency);
  const totals = summaries.reduce((all, row) => ({
    input: all.input + row.known_input_tokens, output: all.output + row.known_output_tokens,
    unknown: all.unknown + row.unknown_requests, unpriced: all.unpriced + row.unpriced_requests,
  }), { input: 0, output: 0, unknown: 0, unpriced: 0 });

  return <>
    <Toolbar title="用量与预算" subtitle="服务返回、估算与未知费用分开记录"
      actions={<>
        <Button icon={<ReloadOutlined aria-hidden="true" />} onClick={() => reload().catch(error => message.error(error.message))}>刷新</Button>
        <Button type="primary" onClick={() => { choose('CNY'); setEditing(true); }}>设置自定义预算</Button>
      </>} />
    <Page wide>
      <div className="stat-tiles" aria-label="用量概览">
        <div className="stat-tile">
          <span>已知费用</span>
          {priced.length === 0 ? <strong>—</strong> : priced.map(row => <strong key={row.currency}>{symbol(row.currency) || `${row.currency} `}{money(row.cost_micros)}</strong>)}
          <small>按价格快照计算</small>
        </div>
        <div className="stat-tile">
          <span>未确认预留</span>
          {priced.length === 0 ? <strong>—</strong> : priced.map(row => <strong key={row.currency}>{row.unpriced_requests > 0 && row.held_micros === 0 ? '待确认' : `${symbol(row.currency)}${money(row.held_micros)}`}</strong>)}
          <small>失败或用量不完整时保留</small>
        </div>
        <div className="stat-tile">
          <span>已报告 tokens</span>
          <strong>{count(totals.input)}<small> 输入</small></strong>
          <strong>{count(totals.output)}<small> 输出</small></strong>
        </div>
        <div className="stat-tile">
          <span>未知用量 / 费用未确认</span>
          <strong className={totals.unknown > 0 ? 'is-warning' : undefined}>{totals.unknown}<small> 次未知用量</small></strong>
          <strong className={totals.unpriced > 0 ? 'is-warning' : undefined}>{totals.unpriced}<small> 次费用未确认</small></strong>
          <small>不按零费用处理</small>
        </div>
      </div>

      <Group title="按币种明细" extra={<InfoTip title="自定义预算与服务商余额、订阅额度分别计算">价格来自你填写的快照。请求前进行保守预约；失败或未返回完整用量时保留未确认预留。历史未知费用单独列出，不作为已知零费用。</InfoTip>}>
        <Table<Summary> className="flush-table" size="small" rowKey={row => row.currency || 'unknown'} pagination={false} dataSource={summaries} scroll={{ x: 900 }}
          locale={{ emptyText: '还没有用量记录' }} columns={[
            { title: '币种', width: 90, render: (_, row) => row.currency || '币种未知' },
            { title: '已知费用', width: 120, render: (_, row) => row.currency ? money(row.cost_micros) : '未知' },
            { title: '未确认预留估算', width: 140, render: (_, row) => row.currency ? row.unpriced_requests > 0 && row.held_micros === 0 ? '待确认' : money(row.held_micros) : '无法估算' },
            { title: '已报告输入 / 输出 tokens', width: 180, render: (_, row) => `${row.known_input_tokens} / ${row.known_output_tokens}` },
            { title: '估算输入 / 输出 tokens', width: 170, render: (_, row) => `${row.estimated_input_tokens} / ${row.estimated_output_tokens}` },
            { title: '未知用量请求', width: 110, dataIndex: 'unknown_requests' },
            { title: '费用未确认请求', width: 120, dataIndex: 'unpriced_requests' },
          ]} />
      </Group>

      <div className="two-col groups">
        <Group title="自定义预算" footer="启用后，价格未知的模型请求会停止在预约阶段。">
          {(data?.budgets || []).length === 0
            ? <div className="row-empty">尚未启用本地预算。你可以先为连接填写价格，再设置对应币种的上限。</div>
            : data.budgets.map((budget: any) => <Row key={budget.currency} label={`${budget.currency} 上限`}>
              <span className="value-text mono">{money(budget.limit_micros)}</span>
              <Status tone={budget.enabled ? 'green' : 'gray'}>{budget.enabled ? '已启用' : '已停用'}</Status>
            </Row>)}
        </Group>
        <Group title="服务商额度" footer="不会将两者合并成 API 可用金额。">
          <Row label="API 服务余额"><Status tone="gray">未知</Status></Row>
          <Row label="GPT / Claude 订阅额度"><Status tone="gray">尚未接入</Status></Row>
        </Group>
      </div>
    </Page>
    <Modal title="设置本地自定义预算" open={editing} onCancel={() => !busy && setEditing(false)} onOk={save} confirmLoading={busy} okText="保存预算" cancelText="取消" width={440}>
      <Form form={form} layout="vertical" className="sheet-form">
        <Form.Item name="currency" label="预算币种"><Select onChange={choose} options={[{ value: 'CNY', label: '人民币 CNY' }, { value: 'USD', label: '美元 USD' }]} /></Form.Item>
        <Form.Item name="enabled" label="启用此币种预算" valuePropName="checked"><Switch size="small" /></Form.Item>
        <Form.Item name="amount" label="上限金额" rules={[{ required: true, type: 'number', min: 0, message: '请填写非负金额；启用时需要大于零' }]}><InputNumber min={0} max={1000000} precision={6} style={{ width: '100%' }} /></Form.Item>
        <p className="form-note">启用后，价格未知的模型请求会停止在预约阶段。历史未知费用不包含在已知费用合计中。</p>
      </Form>
    </Modal>
  </>;
}

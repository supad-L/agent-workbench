import { it, expect, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { App, ConfigProvider } from 'antd';
import Usage from './usage';

it('未知费用保留待确认状态，不把缺失价格显示为零预留结论', async () => {
  window.workbench = { invoke: vi.fn(async () => ({ ok: true, value: { summaries: [{ currency: 'CNY', cost_micros: 0, held_micros: 0, known_input_tokens: 20, known_output_tokens: 10, estimated_input_tokens: 0, estimated_output_tokens: 0, unknown_requests: 0, unpriced_requests: 2 }], budgets: [] } })), onEvent: () => () => {} };
  render(<ConfigProvider><App><Usage /></App></ConfigProvider>);
  const row = (await screen.findByText('CNY')).closest('tr');
  expect(row).toBeTruthy();
  expect(within(row!).getByText('待确认')).toBeTruthy();
  expect(within(row!).getByText('20 / 10')).toBeTruthy();
  expect(within(row!).getByText('2')).toBeTruthy();
});

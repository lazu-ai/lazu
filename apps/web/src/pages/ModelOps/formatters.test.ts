import { describe, expect, it } from 'vitest';
import {
  formatRate,
  laneNamesOf,
  laneShortName,
  laneTitle,
  poolCostBasis,
} from './formatters';
import type { ModelOpsPoolEntry } from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';

const entry = (
  weight: number,
  costs: Partial<
    Pick<
      ModelOpsPoolEntry,
      | 'cost_input_per_mtok'
      | 'cost_output_per_mtok'
      | 'cost_request_usd'
      | 'cost_second_usd'
    >
  >,
): ModelOpsPoolEntry => ({
  channel_id: `channel-${weight}`,
  channel_internal_id: weight + 1,
  channel_status: 1,
  cost_input_per_mtok: null,
  cost_output_per_mtok: null,
  enabled: true,
  health: {
    avg_latency_ms: null,
    error_count: 0,
    last_outcome_at: null,
    request_count: 0,
    success_rate: null,
  },
  lane: 'direct',
  name: `source-${weight}`,
  priority: 0,
  response_time_ms: 0,
  source_type: 'official',
  type: 1,
  weight,
  ...costs,
});

describe('poolCostBasis', () => {
  it('uses the router weight plus 10 smoothing factor', () => {
    const result = poolCostBasis([
      entry(0, { cost_input_per_mtok: 1 }),
      entry(10, { cost_input_per_mtok: 3 }),
    ]);
    expect(result.input).toBeCloseTo(7 / 3);
  });

  it('reduces per-call and per-second provider costs', () => {
    const result = poolCostBasis([
      entry(0, { cost_request_usd: 0.1, cost_second_usd: 0.01 }),
      entry(10, { cost_request_usd: 0.4, cost_second_usd: 0.04 }),
    ]);
    expect(result.request).toBeCloseTo(0.3);
    expect(result.second).toBeCloseTo(0.03);
  });

  it('excludes sources the router cannot select', () => {
    const disabled = entry(100, { cost_input_per_mtok: 99 });
    disabled.enabled = false;
    const result = poolCostBasis([
      entry(0, { cost_input_per_mtok: 1 }),
      disabled,
    ]);
    expect(result.input).toBe(1);
  });
});

describe('formatRate', () => {
  it('prints every rate with one decimal, so a column lines up', () => {
    expect(formatRate(0.931)).toBe('93.1%');
    expect(formatRate(1)).toBe('100.0%');
    expect(formatRate(0.9997)).toBe('100.0%');
    expect(formatRate(0.155)).toBe('15.5%');
    expect(formatRate(null)).toBe('—');
  });
});

describe('lane names', () => {
  // Echoes defaultValue with {{name}} filled, as i18next does for a missing key.
  const t = ((_key: string, options: { defaultValue: string; name?: string }) =>
    options.defaultValue.replace('{{name}}', options.name ?? '')) as TFunction;
  const names = laneNamesOf([
    { slug: 'direct', name: '稳定' },
    { slug: 'cheap', name: 'Agent 专线' },
    { slug: 'blank', name: '  ' },
  ]);

  it('titles a lane from 线路设置 without doubling 线路', () => {
    expect(laneTitle('direct', names, t)).toBe('稳定线路');
    expect(laneTitle('cheap', names, t)).toBe('Agent 专线');
  });

  it('falls back to the builtin copy before the registry loads', () => {
    expect(laneTitle('direct', {}, t)).toBe('稳定线路');
    expect(laneTitle('cheap', {}, t)).toBe('Agent 专线');
    expect(laneTitle('blank', names, t)).toBe('blank线路');
    expect(laneShortName('cheap', {}, t)).toBe('Agent');
  });

  it('labels a lane row with the registry name alone', () => {
    expect(laneShortName('cheap', names, t)).toBe('Agent 专线');
  });
});

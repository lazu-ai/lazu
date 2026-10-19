import { describe, expect, it } from 'vitest';
import { poolCostBasis } from './formatters';
import type { ModelOpsPoolEntry } from '../../hooks/models/useModelOpsQuery';

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

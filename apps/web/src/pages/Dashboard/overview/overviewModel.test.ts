import { describe, expect, it } from 'vitest';
import {
  buildPlatformKpiSublines,
  buildPlatformWatchlist,
  computeWorkspaceRunwayDays,
} from './overviewModel';

describe('overviewModel helpers', () => {
  it('computes workspace runway days without turning undefined balance into 0', () => {
    expect(
      computeWorkspaceRunwayDays(undefined, 7_000_000, { preset: '7d' }),
    ).toBeNull();
    expect(
      computeWorkspaceRunwayDays(5_000_000, 0, { preset: '7d' }),
    ).toBeNull();
    expect(
      computeWorkspaceRunwayDays(4_200_000, 29_400_000, { preset: '7d' }),
    ).toBeCloseTo(1, 5);
  });

  it('builds 24h platform KPI sub-lines with zero placeholder dashes', () => {
    const sublines = buildPlatformKpiSublines({
      totals: {
        request_count: 0,
        outcome_count: 0,
        error_count: 0,
        success_rate: null,
        amount_microusd: 0,
        upstream_cost_microusd: 0,
        gross_margin_microusd: 0,
        prompt_tokens: 0,
        completion_tokens: 0,
        cached_tokens: 0,
        p95_latency_ms: null,
        p95_ttft_ms: null,
      },
      growth: {
        new_users: 4,
        activated: 2,
        paid: 1,
        active_users: 18,
        returned_users: 0,
        churned_users: 0,
        total_customers: 120,
        churn_applicable: false,
        new_users_previous: 3,
        active_users_previous: 15,
      },
      topups: { count: 2, amount_microusd: 50_000_000, recent: [] },
    });

    expect(sublines.marginSub).not.toContain('—');
    expect(sublines.serviceSub).not.toContain('—');
    expect(sublines.growthSub).not.toContain('—');
    expect(sublines.growthSub).toContain('共 120 个用户');
  });

  it('surfaces degraded providers and weak models into actionable watchlist items', () => {
    const watchlist = buildPlatformWatchlist({
      providers: [
        {
          channel_id: 9,
          channel_name: 'OpenRouter Fallback',
          source_type: 'openrouter',
          channel_type: 1,
          status: 'degraded',
          source: 'traffic',
          disabled: false,
          weight: 10,
          request_count: 250,
          error_count: 14,
          success_rate: 0.944,
          model_count: 6,
          lane_count: 6,
          probe_count: 0,
          buckets: [],
          lanes: [],
        },
      ],
      models: [
        {
          id: 0,
          name: 'claude-sonnet-5',
          request_count: 100,
          error_count: 5,
          amount_microusd: 10_000_000,
          prompt_tokens: 1000,
          completion_tokens: 500,
          cached_input_tokens: 200,
          last_used_at: 1700000000,
        },
      ],
    });

    expect(watchlist).toHaveLength(2);
    expect(watchlist[0]).toMatchObject({
      name: 'OpenRouter Fallback',
      to: '/console/health',
      tone: 'warn',
    });
    expect(watchlist[1]).toMatchObject({
      name: 'claude-sonnet-5',
      to: '/console/model-ops',
      mono: true,
    });
  });
});

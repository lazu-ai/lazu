import { describe, expect, it } from 'vitest';
import {
  contextTierFields,
  findSellPriceRow,
  groupSellPrices,
  laneChanged,
  laneHasPrice,
  lanePayload,
  type PricedModel,
  type SellPriceRow,
  laneHasNegativePrice,
  laneBillingMode,
  parseOptionalNumber,
} from './sellPrices';

describe('findSellPriceRow', () => {
  it('uses the normalized price owner returned with a model lane', () => {
    const wildcard = {
      model_name: 'gpt-4o-gizmo-*',
      channel_group: 'direct',
      cache_read_per_mtok: 0.25,
    } as SellPriceRow;

    expect(
      findSellPriceRow(
        [wildcard],
        'gpt-4o-gizmo-custom',
        'direct',
        'gpt-4o-gizmo-*',
      ),
    ).toBe(wildcard);
  });
});

/** A published row carrying dimensions the pricing form never renders. */
const publishedDirect = {
  active: true,
  audio_input_per_mtok: 40,
  cache_read_per_mtok: 0.075,
  channel_group: 'direct',
  created_at: '2026-08-01T00:00:00Z',
  id: 7,
  image_output_per_mtok: 30,
  input_per_mtok: 0.15,
  model_name: 'gpt-image-1',
  output_per_mtok: 0.6,
  pricing_version: 1723000000,
  source: 'litellm_sync',
  updated_at: '2026-08-01T00:00:00Z',
} as SellPriceRow;

const model = (over: Partial<PricedModel> = {}): PricedModel => ({
  eco: { enabled: false },
  model_name: 'gpt-image-1',
  stable: {
    billing_mode: 'per_token',
    enabled: true,
    input_per_million: 0.2,
    output_per_million: 0.6,
  },
  ...over,
});

describe('lanePayload', () => {
  it('carries over dimensions the form does not edit', () => {
    const payload = lanePayload(model(), 'stable', publishedDirect);
    expect(payload.image_output_per_mtok).toBe(30);
    expect(payload.cache_read_per_mtok).toBe(0.075);
    expect(payload.audio_input_per_mtok).toBe(40);
    expect(payload.input_per_mtok).toBe(0.2);
  });

  it('drops server-owned identity and versioning', () => {
    const payload = lanePayload(model(), 'stable', publishedDirect);
    expect(payload).not.toHaveProperty('id');
    expect(payload).not.toHaveProperty('pricing_version');
    expect(payload).not.toHaveProperty('created_at');
    expect(payload).not.toHaveProperty('updated_at');
    expect(payload.source).toBe('manual');
    expect(payload.active).toBe(true);
  });

  it('clears the token prices when the model bills per call', () => {
    const payload = lanePayload(
      model({
        stable: {
          billing_mode: 'per_call',
          enabled: true,
          per_call: 0.04,
        },
      }),
      'stable',
      publishedDirect,
    );
    expect(payload.request_usd).toBe(0.04);
    expect(payload.input_per_mtok).toBeNull();
    expect(payload.output_per_mtok).toBeNull();
    // A per-call image model still bills its image dimension.
    expect(payload.image_output_per_mtok).toBe(30);
  });

  it('maps the agent lane onto its stored channel group', () => {
    const payload = lanePayload(
      model({
        eco: {
          billing_mode: 'per_token',
          enabled: true,
          input_per_million: 0.1,
        },
      }),
      'eco',
    );
    expect(payload.channel_group).toBe('cheap');
    expect(payload.model_name).toBe('gpt-image-1');
  });
});

describe('laneHasPrice', () => {
  it('is false for a lane with no price, so it is never published empty', () => {
    expect(laneHasPrice({ enabled: false }, 'per_token')).toBe(false);
    expect(laneHasPrice(undefined, 'per_token')).toBe(false);
  });

  it('ignores prices that belong to another billing mode', () => {
    expect(laneHasPrice({ per_call: 0.04 }, 'per_token')).toBe(false);
    expect(laneHasPrice({ per_call: 0.04 }, 'per_call')).toBe(true);
  });
});

describe('laneChanged', () => {
  it('is false when the operator did not move the lane', () => {
    const unchanged = model({
      stable: {
        billing_mode: 'per_token',
        enabled: true,
        input_per_million: 0.15,
        output_per_million: 0.6,
      },
    });
    expect(laneChanged(unchanged, 'stable', publishedDirect)).toBe(false);
  });

  it('is true for an edited price and for a brand-new lane', () => {
    expect(laneChanged(model(), 'stable', publishedDirect)).toBe(true);
    expect(laneChanged(model(), 'eco')).toBe(true);
  });
});

describe('groupSellPrices', () => {
  it('keeps the newest version of each lane', () => {
    const rows = [
      { ...publishedDirect, pricing_version: 2, input_per_mtok: 0.2 },
      { ...publishedDirect, pricing_version: 1, active: false },
      {
        active: true,
        channel_group: 'cheap',
        input_per_mtok: 0.1,
        model_name: 'gpt-image-1',
      },
    ] as SellPriceRow[];
    const [grouped] = groupSellPrices(rows);
    expect(grouped.stable?.input_per_million).toBe(0.2);
    expect(grouped.eco?.input_per_million).toBe(0.1);
    expect(grouped.eco?.enabled).toBe(true);
    expect(laneBillingMode(grouped, 'stable')).toBe('per_token');
    expect(laneBillingMode(grouped, 'eco')).toBe('per_token');
  });

  it('keeps different billing modes on each lane', () => {
    const [grouped] = groupSellPrices([
      { ...publishedDirect, request_usd: 0.04, input_per_mtok: null },
      {
        active: true,
        channel_group: 'cheap',
        model_name: 'gpt-image-1',
        second_usd: 0.01,
      },
    ]);
    expect(laneBillingMode(grouped, 'stable')).toBe('per_call');
    expect(laneBillingMode(grouped, 'eco')).toBe('per_second');
  });
});

describe('parseOptionalNumber', () => {
  it('distinguishes a cleared field from invalid numeric text', () => {
    expect(parseOptionalNumber('')).toEqual({ valid: true, value: null });
    expect(parseOptionalNumber('1.25')).toEqual({ valid: true, value: 1.25 });
    expect(parseOptionalNumber('abc')).toEqual({ valid: false, value: null });
    expect(parseOptionalNumber('', true)).toEqual({
      valid: false,
      value: null,
    });
  });
});

describe('laneHasNegativePrice', () => {
  it('flags negative token prices', () => {
    expect(laneHasNegativePrice({ input_per_million: -1 }, 'per_token')).toBe(
      true,
    );
  });

  it('accepts zero and positive prices', () => {
    expect(
      laneHasNegativePrice(
        { input_per_million: 0, output_per_million: 15 },
        'per_token',
      ),
    ).toBe(false);
  });

  it('flags a negative peak multiplier', () => {
    expect(
      laneHasNegativePrice(
        { input_per_million: 1, peak_multiplier: -2 },
        'per_token',
      ),
    ).toBe(true);
  });
});

// Cache prices now have explicit fields, while unrelated price dimensions survive.
it('applies cache edits and explicit clears without dropping image prices', () => {
  const edited = model({
    stable: {
      input_per_million: 0.2,
      cache_read_per_mtok: 0.05,
      cache_write_5m_per_mtok: null,
      cache_write_1h_per_mtok: 0.8,
    },
  });
  const payload = lanePayload(edited, 'stable', publishedDirect);
  expect(payload.cache_read_per_mtok).toBe(0.05);
  expect(payload.cache_write_5m_per_mtok).toBeNull();
  expect(payload.cache_write_1h_per_mtok).toBe(0.8);
  expect(payload.image_output_per_mtok).toBe(30);
});

describe('contextTierFields', () => {
  it('keeps a tier only with a threshold on token pricing', () => {
    expect(
      contextTierFields(
        {
          context_tier_input_per_mtok: 4,
          context_tier_output_per_mtok: 15,
          context_tier_threshold: 272_000,
        },
        'per_token',
      ),
    ).toMatchObject({
      context_tier_input_per_mtok: 4,
      context_tier_output_per_mtok: 15,
      context_tier_threshold: 272_000,
    });
  });

  it('clears the tier prices when the threshold is gone', () => {
    expect(
      contextTierFields(
        { context_tier_input_per_mtok: 4, context_tier_threshold: 0 },
        'per_token',
      ),
    ).toMatchObject({
      context_tier_input_per_mtok: null,
      context_tier_threshold: 0,
    });
  });

  it('drops the tier for per-call pricing', () => {
    expect(
      contextTierFields(
        { context_tier_input_per_mtok: 4, context_tier_threshold: 200_000 },
        'per_call',
      ).context_tier_threshold,
    ).toBe(0);
  });
});

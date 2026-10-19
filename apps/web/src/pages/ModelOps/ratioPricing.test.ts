import { describe, expect, it } from 'vitest';
import {
  derivedPrice,
  draftFromRow,
  lanePayload,
  officialOf,
  validateDraft,
} from './ratioPricing';
import type { SellPriceRow } from './sellPrices';

const official = officialOf({
  official_input_price: 3 / 1e6,
  official_output_price: 15,
  official_cache_read_per_mtok: 0.3,
  official_cache_write_per_mtok: 3.75,
});

describe('ratio pricing', () => {
  it('reads both official units per million', () => {
    expect(official).toMatchObject({ input: 3, output: 15, cacheRead: 0.3 });
    expect(officialOf({ official_input_price: 0 })).toBeNull();
  });

  it('derives from the lane ratio and keeps pinned prices', () => {
    const draft = draftFromRow(
      {
        model_name: 'm',
        channel_group: 'cheap',
        follow_official: true,
        pinned_fields: 'output',
        input_per_mtok: 1.5,
        output_per_mtok: 9,
      },
      {},
      official,
    );
    expect(draft.mode).toBe('follow');
    expect(draft.pins).toEqual({ output: '9' });
    expect(derivedPrice(draft, official, 50)).toEqual({
      input: 1.5,
      output: 9,
      cache_read: 0.15,
      cache_write_5m: 1.875,
    });
    expect(derivedPrice({ ...draft, ratio: '60' }, official, 50).input).toBe(
      1.8,
    );
  });

  it('carries the fields it does not edit when it saves', () => {
    const current: SellPriceRow = {
      id: 7,
      model_name: 'gpt-image-2',
      channel_group: 'direct',
      request_usd: 0.04,
      image_output_per_mtok: 40,
      pricing_version: 3,
    } as SellPriceRow;
    const draft = draftFromRow(current, {}, null);
    expect(draft.mode).toBe('fixed');
    const payload = lanePayload(draft, current, 'direct', 'gpt-image-2');
    expect(payload.request_usd).toBe(0.04);
    expect(payload.image_output_per_mtok).toBe(40);
    expect(payload.follow_official).toBe(false);
    expect(payload.id).toBeUndefined();
  });

  it('sends pins and the ratio for a following lane', () => {
    const draft = {
      ...draftFromRow(undefined, {}, official),
      exists: true,
      ratio: '50',
      pins: { output: '2' },
    };
    const payload = lanePayload(draft, undefined, 'cheap', 'gpt-6-luna');
    expect(payload).toMatchObject({
      follow_official: true,
      price_ratio: 50,
      pinned_fields: 'output',
      output_per_mtok: 2,
      channel_group: 'cheap',
    });
    expect(validateDraft({ ...draft, ratio: '0' })).toBe('ratio');
    expect(validateDraft({ ...draft, pins: { input: '' } })).toBe('pin');
  });
});

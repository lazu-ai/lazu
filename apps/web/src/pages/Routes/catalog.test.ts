import { describe, expect, it } from 'vitest';
import {
  applyLanePeak,
  contextLengthOf,
  discountMarkOf,
  formatContextLength,
  healthTone,
  heroPriceOf,
  laneOfficialRatio,
  planZheOf,
  lanePeakFactor,
  peakActiveNow,
  readCatalogItems,
  resolveLanes,
  type CatalogModel,
} from './catalog';

describe('model catalog normalization', () => {
  it('reads the registered catalog response shape', () => {
    expect(readCatalogItems({ data: { data: [{ id: 'gpt-test' }] } })).toEqual([
      { id: 'gpt-test' },
    ]);
  });

  it('uses context_length before the legacy context_window alias', () => {
    expect(
      contextLengthOf({
        id: 'gpt-test',
        context_length: 128_000,
        context_window: 32_000,
      }),
    ).toBe(128_000);
  });

  it('uses the catalog-owned stable and agent lanes', () => {
    const model: CatalogModel = {
      id: 'gpt-test',
      lanes: [
        { key: 'stable', pricing: { input_per_million: 1 } },
        { key: 'eco', pricing: { input_per_million: 0.5 } },
      ],
    };

    expect(resolveLanes(model)).toMatchObject([
      { key: 'stable', pricing: { input_per_million: 1 } },
      { key: 'eco', pricing: { input_per_million: 0.5 } },
    ]);
  });

  it("keeps each lane's own price for the viewer", () => {
    const model: CatalogModel = {
      id: 'gpt-test',
      lanes: [
        {
          key: 'stable',
          pricing: { input_per_million: 1 },
          your_price: { input_per_million: 0.9 },
        },
        {
          key: 'eco',
          pricing: { input_per_million: 0.5 },
          your_price: { input_per_million: 0.25 },
        },
      ],
    };

    expect(resolveLanes(model)).toMatchObject([
      { key: 'stable', your_price: { input_per_million: 0.9 } },
      { key: 'eco', your_price: { input_per_million: 0.25 } },
    ]);
  });

  it('matches the backend half-open peak-window semantics', () => {
    expect(peakActiveNow('1-4', new Date('2026-08-21T03:00:00Z'))).toBe(true);
    expect(peakActiveNow('1-4', new Date('2026-08-21T04:00:00Z'))).toBe(false);
    expect(peakActiveNow('22-2', new Date('2026-08-21T01:00:00Z'))).toBe(true);
    expect(peakActiveNow('22-2', new Date('2026-08-21T02:00:00Z'))).toBe(false);
    expect(peakActiveNow('25-1', new Date('2026-08-21T00:00:00Z'))).toBe(false);
  });

  it('applies the active lane multiplier to displayed prices', () => {
    const lane = {
      key: 'eco' as const,
      peak_hours_utc: '1-4',
      peak_multiplier: 1.5,
    };
    const peakTime = new Date('2026-08-21T03:00:00Z');
    const baseTime = new Date('2026-08-21T05:00:00Z');

    expect(lanePeakFactor(lane, peakTime)).toBe(1.5);
    expect(applyLanePeak(2, lane, peakTime)).toBe(3);
    expect(applyLanePeak(2, lane, baseTime)).toBe(2);
  });

  it('lets an explicit operational status override a misleading availability', () => {
    expect(healthTone({ status: 'unavailable', availability: 1 })).toBe('bad');
    expect(healthTone({ status: 'healthy' })).toBe('ok');
  });

  it('trusts the published status over a window that still holds an outage', () => {
    expect(healthTone({ status: 'available', availability: 0.9 })).toBe('ok');
    expect(healthTone({ availability: 0.9 })).toBe('bad');
  });
});

describe('formatContextLength', () => {
  it('reads million-token windows as M, not four digits of k', () => {
    expect(formatContextLength(1_000_000)).toBe('1M');
    // Vendors publish 1,048,576 and 1,000,000 for the same "1M" window.
    expect(formatContextLength(1_048_576)).toBe('1M');
    expect(formatContextLength(2_000_000)).toBe('2M');
    expect(formatContextLength(1_500_000)).toBe('1.5M');
    // A published 1,050,000 window is not "1M" and not "1.1M".
    expect(formatContextLength(1_050_000)).toBe('1.05M');
  });

  it('keeps smaller windows in k', () => {
    expect(formatContextLength(200_000)).toBe('200k');
    expect(formatContextLength(8_192)).toBe('8k');
  });
});

describe('heroPriceOf', () => {
  const model = { id: 'demo' } as CatalogModel;

  it('leads with input and keeps output beside it', () => {
    const hero = heroPriceOf(
      { key: 'stable', input_per_million: 3, output_per_million: 15 } as never,
      model,
    );
    expect(hero).toMatchObject({ kind: 'input', list: 3, listOut: 15 });
  });

  it('leads with a whole per-call price and drops the token dimensions', () => {
    const hero = heroPriceOf(
      { key: 'stable', per_call: 0.04, output_per_million: 15 } as never,
      model,
    );
    expect(hero).toMatchObject({ kind: 'perCall', list: 0.04 });
    expect(hero?.listOut).toBeUndefined();
  });

  it('leads with output when that is the only price the lane has', () => {
    // The card hides the lane row once the hero prints its price, so a lane
    // priced only on output must still produce a hero or the card shows none.
    expect(
      heroPriceOf({ key: 'stable', output_per_million: 15 } as never, model),
    ).toMatchObject({ kind: 'output', list: 15 });
  });

  it('has no hero for a lane with no price at all', () => {
    expect(heroPriceOf({ key: 'stable' } as never, model)).toBeNull();
  });
});

describe('lane discount mark', () => {
  const model: CatalogModel = {
    id: 'claude-fable-5-1',
    pricing: { input_per_million: 10, output_per_million: 50 },
  };

  it('reads a lane priced at the official list as the list price', () => {
    const stable = { key: 'stable' as const, input_per_million: 10 };
    expect(discountMarkOf(laneOfficialRatio(stable, model))).toEqual({
      kind: 'list',
    });
  });

  it('prints the agent lane as a share of the official price', () => {
    const eco = { key: 'eco' as const, input_per_million: 2.3 };
    expect(discountMarkOf(laneOfficialRatio(eco, model))).toEqual({
      kind: 'off',
      off: 77,
      zhe: 2.3,
    });
  });

  it('marks the lane itself, never folding in a 专属折扣', () => {
    const stable = {
      key: 'stable' as const,
      input_per_million: 9,
      plan_discount_pct: 40,
      your_price: { input_per_million: 6 },
    };
    expect(discountMarkOf(laneOfficialRatio(stable, model))).toMatchObject({
      kind: 'off',
      zhe: 9,
    });
    expect(planZheOf(stable)).toBe(6);
  });

  it('marks nothing without an official price or above it', () => {
    expect(
      laneOfficialRatio({ key: 'eco', input_per_million: 1 }, { id: 'x' }),
    ).toBeUndefined();
    expect(discountMarkOf(1.4)).toBeNull();
  });
});

it('does not multiply the contract discount by the peak factor', () => {
  const lane = {
    key: 'stable' as const,
    peak_hours_utc: '0-24',
    peak_multiplier: 2,
    pricing: { input: 9 },
    your_price: { input: 6, peak_pricing: { input: 6 } },
  };
  expect(
    heroPriceOf(lane, { id: 'priced' }, new Date('2026-10-01T12:00:00Z')),
  ).toMatchObject({ list: 18, mine: 6 });
});

describe('DeepSeek peak calendar from the server', () => {
  const calendar = { years: [2026], holidays: ['2026-10-01', '2026-10-07'] };
  it.each([
    ['2026-10-20T01:00:00Z', 2],
    ['2026-10-20T04:00:00Z', 1],
    ['2026-10-20T06:00:00Z', 2],
    ['2026-10-20T10:00:00Z', 1],
    ['2026-10-17T02:00:00Z', 1],
    ['2026-10-10T02:00:00Z', 1],
    ['2026-10-01T02:00:00Z', 1],
    ['2026-10-07T02:00:00Z', 1],
    ['2027-02-01T02:00:00Z', 1],
  ])('shows the settlement multiplier at %s', (at, factor) => {
    expect(
      lanePeakFactor(
        {
          key: 'stable',
          peak_hours_utc: '1-4,6-10',
          peak_multiplier: 2,
          peak_calendar: calendar,
        },
        new Date(at),
      ),
    ).toBe(factor);
  });
});

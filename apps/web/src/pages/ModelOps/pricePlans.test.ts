import { describe, expect, it } from 'vitest';
import {
  countChanges,
  draftFromPlan,
  draftProblems,
  emptyDraft,
  endDateToIso,
  formatEndDay,
  payloadFromDraft,
  planStatus,
  previewRow,
  resolveRule,
  vendorRuleSpan,
  zheLabel,
  type PlanDraft,
  type PricePlan,
} from './pricePlans';

const plan: PricePlan = {
  active: true,
  default_pct: null,
  ends_at: null,
  id: 1,
  lanes: 'direct',
  member_count: 2,
  name: '合作方案 A',
  note: '',
  rules: [
    { kind: 'vendor', pct: 40, vendor_id: 'v_openai', vendor_name: 'OpenAI' },
    { kind: 'model', model_name: 'gpt-mini', pct: 10 },
  ],
  starts_at: null,
};

describe('price plan helpers', () => {
  it('reads a percentage off as 折', () => {
    expect(zheLabel(40)).toBe('6 折');
    expect(zheLabel(5)).toBe('95 折');
    expect(zheLabel(12.5)).toBe('87.5 折');
  });

  it('resolves model before vendor before default', () => {
    const draft: PlanDraft = { ...draftFromPlan(plan), defaultPct: '5' };
    expect(
      resolveRule(draft, { model_name: 'gpt-mini', vendor_id: 'v_openai' }),
    ).toMatchObject({ kind: 'model', pct: 10 });
    expect(
      resolveRule(draft, { model_name: 'gpt-big', vendor_id: 'v_openai' }),
    ).toMatchObject({ kind: 'vendor', pct: 40, subject: 'OpenAI' });
    expect(
      resolveRule(draft, { model_name: 'kimi', vendor_id: 'v_moon' }),
    ).toMatchObject({ kind: 'default', pct: 5 });
    expect(
      resolveRule(draftFromPlan(plan), { model_name: 'kimi', vendor_id: 'x' }),
    ).toBeNull();
  });

  it('prices min(lane, official × keep) and never above the lane price', () => {
    const draft = draftFromPlan(plan);
    // Official is per token here (LiteLLM unit) — 2.5e-6 = $2.50 / 1M.
    expect(
      previewRow(draft, {
        lanes: [{ channel_group: 'direct', input_per_mtok: 2 }],
        model_name: 'gpt-big',
        official_input_price: 2.5e-6,
        vendor_id: 'v_openai',
      }),
    ).toMatchObject({ mine: 1.5, official: 2.5, standard: 2 });
    // A lane already cheaper than the plan price keeps its own price.
    expect(
      previewRow(draft, {
        lanes: [{ channel_group: 'direct', input_per_mtok: 1 }],
        model_name: 'gpt-big',
        official_input_price: 2.5,
        vendor_id: 'v_openai',
      })?.mine,
    ).toBe(1);
    // No official price: the percentage comes off the lane price.
    expect(
      previewRow(draft, {
        lanes: [{ channel_group: 'direct', input_per_mtok: 2 }],
        model_name: 'gpt-big',
        vendor_id: 'v_openai',
      })?.mine,
    ).toBeCloseTo(1.2);
    // 仅 Agent 专线 previews the agent lane.
    expect(
      previewRow(
        { ...draft, lanes: 'cheap' },
        {
          lanes: [{ channel_group: 'direct', input_per_mtok: 2 }],
          model_name: 'gpt-big',
          vendor_id: 'v_openai',
        },
      ),
    ).toBeNull();
  });

  it('counts each changed field and rule once', () => {
    const initial = draftFromPlan(plan);
    expect(countChanges(initial, initial)).toBe(0);
    const next: PlanDraft = {
      ...initial,
      lanes: 'all',
      models: [],
      vendors: [
        { ...initial.vendors[0], pct: '30' },
        { pct: '20', vendor_id: 'v_g', vendor_name: 'Google' },
      ],
    };
    // lanes + openai re-priced + google added + model removed
    expect(countChanges(next, initial)).toBe(4);
  });

  it('validates what the server rejects', () => {
    const draft: PlanDraft = {
      ...emptyDraft(),
      defaultPct: '100',
      endsAt: '2026-01-01T00:00:00.000Z',
      startsAt: '2026-02-01T00:00:00.000Z',
      vendors: [{ pct: '0', vendor_id: 'v', vendor_name: 'V' }],
    };
    expect(draftProblems(draft).sort()).toEqual(
      ['dates', 'defaultPct', 'name', 'vendorPct'].sort(),
    );
  });

  it('sends the vendor public id and a null default', () => {
    expect(payloadFromDraft(draftFromPlan(plan))).toMatchObject({
      default_pct: null,
      ends_at: null,
      rules: [
        { kind: 'vendor', pct: 40, vendor_id: 'v_openai' },
        { kind: 'model', model_name: 'gpt-mini', pct: 10 },
      ],
    });
  });

  it('stores an end day as the midnight after it', () => {
    const iso = endDateToIso(new Date(2026, 11, 31));
    expect(new Date(iso).getDate()).toBe(1);
    expect(formatEndDay(iso)).toBe('2026-12-31');
  });

  it('reads status and the vendor span', () => {
    const now = Date.parse('2026-06-01T00:00:00Z');
    expect(planStatus(plan, now)).toBe('live');
    expect(planStatus({ ...plan, active: false }, now)).toBe('off');
    expect(
      planStatus({ ...plan, starts_at: '2026-07-01T00:00:00Z' }, now),
    ).toBe('upcoming');
    expect(planStatus({ ...plan, ends_at: '2026-05-01T00:00:00Z' }, now)).toBe(
      'expired',
    );
    expect(
      vendorRuleSpan([
        ...plan.rules,
        { kind: 'vendor', pct: 5, vendor_id: 'm' },
      ]),
    ).toEqual({ count: 2, range: '5%–40%' });
  });
});

it('zero model exception blocks the vendor discount and keeps the lane price', () => {
  const draft = {
    ...emptyDraft(),
    name: 'Exclusions',
    vendors: [{ vendor_id: 'v', vendor_name: 'V', pct: '40' }],
    models: [{ model_name: 'excluded', pct: '0' }],
  };
  expect(draftProblems(draft)).toEqual([]);
  expect(
    previewRow(draft, {
      model_name: 'excluded',
      vendor_id: 'v',
      input_per_mtok: 12,
      official_input_price: 10,
    }),
  ).toMatchObject({ mine: 12, hit: { pct: 0, kind: 'model' } });
});

import { describe, expect, it } from 'vitest';
import {
  accountKeyOperationalState,
  adjacentAccountTarget,
  buildDailySpendSeries,
} from './account-model';

describe('customer account daily spend series', () => {
  it('keeps a fixed 30-day timeline and pads missing buckets with zero', () => {
    const end = new Date('2026-09-02T00:00:00Z');
    const yesterday = new Date('2026-09-01T00:00:00Z').getTime() / 1000;
    const series = buildDailySpendSeries(
      [{ amount_microusd: 2_500_000, bucket: yesterday }],
      end.getTime() / 1000,
    );

    expect(series).toHaveLength(30);
    expect(series.slice(-3)).toEqual([0, 2_500_000, 0]);
  });

  it('merges duplicate day buckets without changing the timeline', () => {
    const end = new Date('2026-09-02T00:00:00Z');
    const today = end.getTime() / 1000;
    const series = buildDailySpendSeries(
      [
        { amount_microusd: 400_000, bucket: today },
        { amount_microusd: 600_000, bucket: today + 60 },
      ],
      today,
    );

    expect(series[series.length - 1]).toBe(1_000_000);
  });
});

describe('customer account key state', () => {
  it('gives account suspension precedence over an enabled key', () => {
    expect(
      accountKeyOperationalState(
        { status: 2 },
        { status: 1, expired_time: 0 },
        1_800_000_000,
      ),
    ).toBe('account_disabled');
  });

  it('keeps expiry ahead of the persisted enabled state', () => {
    expect(
      accountKeyOperationalState(
        { status: 1 },
        { status: 1, expired_time: 1_700_000_000 },
        1_800_000_000,
      ),
    ).toBe('expired');
  });
});

describe('customer account adjacent navigation', () => {
  const rows = [
    { id: 'acct-21', name: 'Twenty one' },
    { id: 'acct-22', name: 'Twenty two' },
  ];

  it('returns the adjacent row while it is loaded', () => {
    expect(adjacentAccountTarget(rows, 'acct-21', 2, 20, 43, 'next')).toEqual({
      account: rows[1],
      kind: 'row',
    });
  });

  it('continues next navigation on the following page', () => {
    expect(adjacentAccountTarget(rows, 'acct-22', 2, 20, 43, 'next')).toEqual({
      kind: 'page',
      page: 3,
      pick: 'first',
    });
  });

  it('continues previous navigation on the preceding page', () => {
    expect(
      adjacentAccountTarget(rows, 'acct-21', 2, 20, 43, 'previous'),
    ).toEqual({ kind: 'page', page: 1, pick: 'last' });
  });

  it('stops only at the true first and last customers', () => {
    expect(adjacentAccountTarget(rows, 'acct-21', 1, 20, 2, 'previous')).toBe(
      null,
    );
    expect(adjacentAccountTarget(rows, 'acct-22', 1, 20, 2, 'next')).toBe(null);
  });
});

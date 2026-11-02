import { describe, expect, it } from 'vitest';
import { spendBuckets } from './SpendCard';

const local = (y: number, m: number, d: number, h = 0) =>
  new Date(y, m - 1, d, h).getTime() / 1000;

describe('spend card buckets', () => {
  it('folds hours into local days and fills the empty ones', () => {
    const { buckets, hourly } = spendBuckets(
      [
        {
          bucket: local(2026, 10, 27, 9),
          amount_microusd: 100,
          request_count: 1,
        },
        {
          bucket: local(2026, 10, 27, 15),
          amount_microusd: 200,
          request_count: 2,
        },
        {
          bucket: local(2026, 10, 29, 3),
          amount_microusd: 50,
          request_count: 1,
        },
      ],
      local(2026, 10, 27),
      local(2026, 10, 29, 23),
      false,
    );
    expect(hourly).toBe(false);
    expect(buckets.map((bucket) => bucket.amount)).toEqual([300, 0, 50]);
    expect(buckets.map((bucket) => bucket.requests)).toEqual([3, 0, 1]);
  });

  it('keeps a window of a day or less by the hour', () => {
    const start = local(2026, 10, 27, 10);
    const { buckets, hourly } = spendBuckets(
      [{ bucket: start + 3_600, amount_microusd: 7, request_count: 1 }],
      start,
      start + 86_400,
      true,
    );
    expect(hourly).toBe(true);
    expect(buckets[1].amount).toBe(7);
    expect(buckets.every((bucket, i) => i === 1 || bucket.amount === 0)).toBe(
      true,
    );
  });
});

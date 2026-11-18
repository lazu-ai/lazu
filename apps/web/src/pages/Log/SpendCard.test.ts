import { describe, expect, it } from 'vitest';
import {
  buildCostShareCaption,
  computeReceiptTiming,
  formatLedgerAmount,
} from './log-support';
import { formatSpendMoney, spendBuckets } from './SpendCard';
import type { TFunction } from 'i18next';

const local = (y: number, m: number, d: number, h = 0) =>
  new Date(y, m - 1, d, h).getTime() / 1000;

const mockT = ((key: string, options?: Record<string, unknown>) => {
  if (key === 'page.console.log.m3.costShareRest') {
    return `其余 ${String(options?.pct ?? '')}`;
  }
  return key;
}) as unknown as TFunction;

describe('spend card buckets and money formatting', () => {
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

  it('formats range spend consistently with table footer spend', () => {
    expect(formatSpendMoney(0)).toBe('$0.00');
    expect(formatSpendMoney(48_620_000)).toBe('$48.62');
    expect(formatSpendMoney(4_100)).toBe('$0.0041');
  });
});

describe('expanded log receipt polish fixes', () => {
  it('uses generationSeconds for streamed requests and relaxes firstFraction clamp to 0.8', () => {
    const streamed = computeReceiptTiming({
      duration: 153,
      firstTokenMs: 144_700,
      isStream: true,
      outputTokens: 10_995,
    });
    expect(streamed.streamed).toBe(true);
    expect(streamed.generationSeconds).toBeCloseTo(8.3, 3);
    expect(streamed.firstFraction).toBe(0.8);
    expect(streamed.tokensPerSecond).toBe(Math.round(10_995 / 8.3));

    const nonStream = computeReceiptTiming({
      duration: 12.4,
      firstTokenMs: 0,
      isStream: false,
      outputTokens: 500,
    });
    expect(nonStream.streamed).toBe(false);
    expect(nonStream.generationSeconds).toBe(12.4);
    expect(nonStream.firstFraction).toBe(0);
  });

  it('formats sub-$0.0001 line items as <$0.0001 with 6-decimal tooltip and keeps 4 decimals elsewhere', () => {
    // 3 input tokens at $0.10/M => 0.3 microusd truncated to 0 in backend
    expect(
      formatLedgerAmount({
        amountMicrousd: 0,
        dimension: 'input',
        rawQuantity: 3,
        unitMicrousd: 100_000,
      }),
    ).toEqual({
      display: '<$0.0001',
      title: '$0.000000',
    });

    // 24 microusd => $0.000024 (< $0.0001)
    expect(
      formatLedgerAmount({
        amountMicrousd: 24,
        dimension: 'cache_read',
        rawQuantity: 2_431,
        unitMicrousd: 10_000,
      }),
    ).toEqual({
      display: '<$0.0001',
      title: '$0.000024',
    });

    // Standard 4-decimal amounts with optional exact 6-decimal title
    expect(formatLedgerAmount({ amountMicrousd: 5_498 })).toEqual({
      display: '$0.0055',
      title: '$0.005498',
    });
    expect(formatLedgerAmount({ amountMicrousd: 4_100 })).toEqual({
      display: '$0.0041',
      title: undefined,
    });
    expect(formatLedgerAmount({ amountMicrousd: 0 })).toEqual({
      display: '$0.0000',
      title: undefined,
    });
  });

  it('summarizes top cost-bar dimensions and remaining <1% share cleanly', () => {
    const caption = buildCostShareCaption(
      [
        {
          amountMicrousd: 0,
          dimension: 'input',
          label: '输入',
          rawQuantity: 3,
          unitMicrousd: 100_000,
        },
        {
          amountMicrousd: 24,
          dimension: 'cache_read',
          label: '缓存读取',
          rawQuantity: 2_431,
          unitMicrousd: 10_000,
        },
        {
          amountMicrousd: 1_387,
          dimension: 'cache_write_5m',
          label: '缓存写入 · 5 分钟',
          rawQuantity: 11_096,
          unitMicrousd: 125_000,
        },
        {
          amountMicrousd: 5_498,
          dimension: 'output',
          label: '输出',
          rawQuantity: 10_995,
          unitMicrousd: 500_000,
        },
      ],
      24 + 1_387 + 5_498,
      mockT,
    );
    expect(caption).toEqual({
      primary: '输出 80% ($0.0055)',
      secondary: '缓存写入 · 5 分钟 20% ($0.0014) · 其余 <1%',
    });
  });
});

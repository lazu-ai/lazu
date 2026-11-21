import dayjs from 'dayjs';
import { describe, expect, it } from 'vitest';
import {
  billMonthKeys,
  dayBarHeight,
  isDayKey,
  isMonthKey,
  monthBarHeight,
  monthRange,
  summarizeMonth,
  walletCsv,
} from './billMonths';

describe('wallet bill', () => {
  const now = dayjs('2026-11-19T15:30:00');

  it('covers the current month and the five before it, newest first', () => {
    expect(billMonthKeys(now)).toEqual([
      '2026-11',
      '2026-10',
      '2026-09',
      '2026-08',
      '2026-07',
      '2026-06',
    ]);
    expect(billMonthKeys(dayjs('2026-01-31T10:00:00'), 3)).toEqual([
      '2026-01',
      '2025-12',
      '2025-11',
    ]);
  });

  it('asks a past month for exactly its own days and the current month up to today', () => {
    const past = monthRange('2026-10', now);
    expect(past.isCurrent).toBe(false);
    expect(past.start).toBe(dayjs('2026-10-01T00:00:00').unix());
    expect(past.end).toBe(dayjs('2026-11-01T00:00:00').unix());

    const current = monthRange('2026-11', now);
    expect(current.isCurrent).toBe(true);
    expect(current.start).toBe(dayjs('2026-11-01T00:00:00').unix());
    expect(current.end).toBe(dayjs('2026-11-20T00:00:00').unix());
    // The ledger endpoint refuses more than 32 days.
    expect(past.end - past.start).toBeLessThanOrEqual(32 * 86_400);
  });

  it('sums a month and carries the opening balance when nothing moved', () => {
    const month = summarizeMonth('2026-11', {
      items: [
        {
          closing_balance_microusd: 21_500_000,
          credit_microusd: 10_000_000,
          date: '2026-11-12',
          spend_microusd: 500_000,
        },
        {
          closing_balance_microusd: 20_000_000,
          credit_microusd: 0,
          date: '2026-11-13',
          spend_microusd: 1_500_000,
        },
      ],
      opening_balance_microusd: 12_000_000,
    });
    expect(month).toMatchObject({
      closing: 20,
      credit: 10,
      opening: 12,
      spend: 2,
    });
    expect(month.days.map((day) => day.date)).toEqual([
      '2026-11-13',
      '2026-11-12',
    ]);

    const quiet = summarizeMonth('2026-07', {
      items: [],
      opening_balance_microusd: 2_400_000,
    });
    expect(quiet).toMatchObject({
      closing: 2.4,
      credit: 0,
      opening: 2.4,
      spend: 0,
    });
  });

  it('keeps an empty month visible in the charts', () => {
    expect(monthBarHeight(0, 24.3)).toBe(2);
    expect(monthBarHeight(24.3, 24.3)).toBeCloseTo(82);
    expect(monthBarHeight(0, 0)).toBe(2);
    expect(dayBarHeight(3.1, 3.1)).toBe(100);
    expect(dayBarHeight(0, 3.1)).toBe(3);
  });

  it('accepts only real month and day keys from the address', () => {
    expect(isMonthKey('2026-11')).toBe(true);
    expect(isMonthKey('2026-1')).toBe(false);
    expect(isMonthKey(null)).toBe(false);
    expect(isDayKey('2026-11-19')).toBe(true);
    expect(isDayKey('2026-11-19T00')).toBe(false);
  });

  it('quotes CSV cells that need it', () => {
    expect(
      walletCsv(
        ['Date', 'Note'],
        [
          ['2026-11-12', 'a,b'],
          ['2026-11-13', 'say "hi"'],
        ],
      ),
    ).toBe('Date,Note\n2026-11-12,"a,b"\n2026-11-13,"say ""hi"""');
  });
});

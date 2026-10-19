import { describe, expect, it } from 'vitest';
import {
  resolveTopupCreditUSD,
  resolveTopupEffectiveTimestamp,
  resolveTopupPaidUSD,
} from './walletAmounts';

describe('wallet top-up amounts', () => {
  it('uses credited micro-USD so bonus credit is preserved', () => {
    expect(
      resolveTopupCreditUSD({
        amount: 100,
        credited_microusd: 110_000_000,
      }),
    ).toBe(110);
  });

  it('falls back to the order amount before the credit settles', () => {
    expect(resolveTopupCreditUSD({ amount: 100 })).toBe(100);
  });

  it('keeps the paid amount separate from credited value', () => {
    expect(resolveTopupPaidUSD({ amount: 100, paid_amount: 100 })).toBe(100);
  });

  it('dates successful top-ups by completion time', () => {
    expect(
      resolveTopupEffectiveTimestamp({
        complete_time: 200,
        create_time: 100,
        status: 'success',
      }),
    ).toBe(200);
  });

  it('falls back to creation time for legacy or pending records', () => {
    expect(
      resolveTopupEffectiveTimestamp({
        complete_time: 0,
        create_time: 100,
        status: 'success',
      }),
    ).toBe(100);
    expect(
      resolveTopupEffectiveTimestamp({
        complete_time: 200,
        create_time: 100,
        status: 'pending',
      }),
    ).toBe(100);
  });
});

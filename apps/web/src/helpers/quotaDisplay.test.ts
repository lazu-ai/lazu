import { describe, expect, it } from 'vitest';
import { buildQuotaDisplay } from './quotaDisplay';

describe('buildQuotaDisplay', () => {
  it('calculates finite quota as used over total plus percent', () => {
    const quota = buildQuotaDisplay({
      remaining: 11_000_000,
      used: 2_500_000,
    });

    expect(quota.mode).toBe('finite');
    expect(quota.remaining).toBe(11_000_000);
    expect(quota.remainingPercent).toBeCloseTo(81.4815, 4);
    expect(quota.used).toBe(2_500_000);
    expect(quota.total).toBe(13_500_000);
    expect(quota.usedPercent).toBeCloseTo(18.5185, 4);
  });

  it('clamps finite quota usage percent for over-consumed balances', () => {
    const quota = buildQuotaDisplay({
      remaining: -1_000_000,
      used: 11_000_000,
    });

    expect(quota.mode).toBe('finite');
    expect(quota.remainingPercent).toBe(0);
    expect(quota.total).toBe(10_000_000);
    expect(quota.usedPercent).toBe(100);
  });

  it('does not show an available share when a finite quota is empty', () => {
    const quota = buildQuotaDisplay({
      remaining: 0,
      used: 0,
    });

    expect(quota.remainingPercent).toBe(0);
    expect(quota.usedPercent).toBe(0);
  });

  it('does not invent a total for unlimited quota', () => {
    const quota = buildQuotaDisplay({
      remaining: -3_000_000,
      unlimited: true,
      used: 8_000_000,
    });

    expect(quota.mode).toBe('unlimited');
    expect(quota.total).toBeNull();
    expect(quota.remaining).toBe(-3_000_000);
    expect(quota.used).toBe(8_000_000);
  });
});

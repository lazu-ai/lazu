import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { asMicroUSD, formatMoney, microUSDToUSD, usdToMicroUSD } from './money';

let rate: string | null = null;

beforeEach(() => {
  rate = null;
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => (key === 'micro_usd_per_usd' ? rate : null),
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('formatMoney', () => {
  it('renders microUSD as dollars without trailing zeros', () => {
    expect(formatMoney(asMicroUSD(0))).toBe('$0');
    expect(formatMoney(asMicroUSD(1_000_000))).toBe('$1');
    expect(formatMoney(asMicroUSD(1_234_567))).toBe('$1.23');
    expect(formatMoney(asMicroUSD(1_234_567), 6)).toBe('$1.234567');
    expect(formatMoney(null)).toBe('$0');
    expect(formatMoney(undefined)).toBe('$0');
  });

  it('shows the smallest step for a positive amount that rounds to zero', () => {
    expect(formatMoney(asMicroUSD(1))).toBe('$0.01');
    expect(formatMoney(asMicroUSD(1), 6)).toBe('$0.000001');
    expect(formatMoney(asMicroUSD(4_000))).toBe('$0.01');
  });

  it('keeps the sign of a negative amount after the dollar sign', () => {
    expect(formatMoney(asMicroUSD(-1_500_000))).toBe('$-1.5');
    expect(formatMoney(asMicroUSD(-1))).toBe('$0');
  });

  it('follows the site rate from localStorage', () => {
    rate = '500000';
    expect(formatMoney(asMicroUSD(1_000_000))).toBe('$2');
    expect(microUSDToUSD(asMicroUSD(250_000))).toBe(0.5);
    expect(usdToMicroUSD(3)).toBe(1_500_000);
    rate = 'garbage';
    expect(formatMoney(asMicroUSD(1_000_000))).toBe('$1');
  });
});

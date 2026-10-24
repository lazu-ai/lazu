import { describe, expect, it } from 'vitest';
import { getDefaultUsageLogDateRange, toUnixRange } from './usageLogDateRange';

const at = (hour: number, minute: number) =>
  new Date(2026, 9, 22, hour, minute);

describe('usage-log default range', () => {
  it('is one whole local day at any hour, including the last one', () => {
    for (const now of [
      at(0, 0),
      at(15, 0),
      at(23, 0),
      at(23, 30),
      at(23, 59),
    ]) {
      const range = toUnixRange(getDefaultUsageLogDateRange(now));
      expect(range.start).toBe(new Date(2026, 9, 22).getTime() / 1000);
      expect(range.end).toBe(new Date(2026, 9, 23).getTime() / 1000);
    }
  });

  it('does not rewrite the dates it rounds', () => {
    const from = at(9, 15);
    const to = at(18, 45);
    toUnixRange({ from, to });
    expect(from.getHours()).toBe(9);
    expect(to.getHours()).toBe(18);
  });

  it('keeps an exact range exact', () => {
    const range = toUnixRange({ exact: true, from: at(9, 0), to: at(10, 0) });
    expect(range.end - range.start).toBe(3600);
  });
});

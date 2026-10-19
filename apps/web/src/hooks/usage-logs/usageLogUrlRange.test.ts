import { describe, expect, it } from 'vitest';
import { getUsageLogUrlRange } from './usageLogUrlRange';

describe('getUsageLogUrlRange', () => {
  it('hydrates a wallet ledger day into the log date filter', () => {
    const range = getUsageLogUrlRange(
      '?tab=logs&start_timestamp=1787328000&end_timestamp=1787414399',
    );

    expect(range?.from.getTime()).toBe(1_787_328_000_000);
    expect(range?.to.getTime()).toBe(1_787_414_399_000);
  });

  it('rejects missing, reversed, or invalid ranges', () => {
    expect(getUsageLogUrlRange('?tab=logs')).toBeNull();
    expect(
      getUsageLogUrlRange('?start_timestamp=20&end_timestamp=10'),
    ).toBeNull();
    expect(
      getUsageLogUrlRange('?start_timestamp=nope&end_timestamp=10'),
    ).toBeNull();
  });
});

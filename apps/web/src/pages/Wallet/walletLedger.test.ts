import { describe, expect, it } from 'vitest';
import { buildDailyWalletLedger } from './walletLedger';

describe('daily wallet ledger', () => {
  it('formats authoritative daily aggregates newest first', () => {
    const rows = buildDailyWalletLedger([
      {
        closing_balance_microusd: 9_000_000,
        credit_microusd: 1_000_000,
        date: '2026-08-01',
        spend_microusd: 2_000_000,
      },
      {
        closing_balance_microusd: 9_250_000,
        credit_microusd: 500_000,
        date: '2026-08-02',
        spend_microusd: 250_000,
      },
    ]);

    expect(rows).toHaveLength(2);
    expect(rows[1]).toMatchObject({
      balance: 9,
      credit: 1,
      spend: 2,
    });
    expect(rows[0]).toMatchObject({
      balance: 9.25,
      credit: 0.5,
      spend: 0.25,
    });
  });

  it('drops malformed aggregate dates instead of rendering invalid rows', () => {
    const rows = buildDailyWalletLedger([
      {
        closing_balance_microusd: 4_000_000,
        date: 'invalid',
        spend_microusd: 1_000_000,
      },
    ]);
    expect(rows).toEqual([]);
  });
});

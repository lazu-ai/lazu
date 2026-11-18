import { describe, expect, it } from 'vitest';
import {
  buildDailyWalletLedger,
  reconcileWalletStatement,
  resolveWalletRouteState,
} from './walletLedger';

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

describe('reconcileWalletStatement', () => {
  it('reconciles current period closing balance with live wallet balance', () => {
    const rows = buildDailyWalletLedger([
      {
        closing_balance_microusd: 4_350_000,
        credit_microusd: 0,
        date: '2026-11-18',
        spend_microusd: 900_000,
      },
    ]);
    const statement = reconcileWalletStatement({
      openingBalanceMicrousd: 5_250_000,
      ledgerRows: rows,
      period: 'current',
      liveWalletBalanceUSD: 4.2,
    });

    expect(statement).toEqual({
      opening: 5.25,
      credits: 0,
      spend: 1.05,
      closing: 4.2,
    });
  });

  it('absorbs unindexed live top-up credit into credits without negative spend', () => {
    const rows = buildDailyWalletLedger([
      {
        closing_balance_microusd: 4_750_000,
        credit_microusd: 0,
        date: '2026-11-18',
        spend_microusd: 250_000,
      },
    ]);
    const statement = reconcileWalletStatement({
      openingBalanceMicrousd: 5_000_000,
      ledgerRows: rows,
      period: 'current',
      liveWalletBalanceUSD: 14.75,
    });

    expect(statement).toEqual({
      opening: 5,
      credits: 10,
      spend: 0.25,
      closing: 14.75,
    });
  });

  it('uses ledger closing balance for previous period', () => {
    const rows = buildDailyWalletLedger([
      {
        closing_balance_microusd: 8_500_000,
        credit_microusd: 5_000_000,
        date: '2026-10-31',
        spend_microusd: 1_500_000,
      },
    ]);
    const statement = reconcileWalletStatement({
      openingBalanceMicrousd: 5_000_000,
      ledgerRows: rows,
      period: 'previous',
      liveWalletBalanceUSD: 2.1,
    });

    expect(statement).toEqual({
      opening: 5,
      credits: 5,
      spend: 1.5,
      closing: 8.5,
    });
  });
});

describe('resolveWalletRouteState', () => {
  it('defaults to topup tab and opens billing orders when owner is present for root', () => {
    expect(resolveWalletRouteState(new URLSearchParams(), false)).toEqual({
      tab: 'topup',
      billingView: 'daily',
      ownerParam: '',
    });
    expect(
      resolveWalletRouteState(new URLSearchParams('tab=billing'), false),
    ).toEqual({
      tab: 'billing',
      billingView: 'daily',
      ownerParam: '',
    });
    expect(
      resolveWalletRouteState(new URLSearchParams('owner=customer_1'), true),
    ).toEqual({
      tab: 'billing',
      billingView: 'orders',
      ownerParam: 'customer_1',
    });
  });
});

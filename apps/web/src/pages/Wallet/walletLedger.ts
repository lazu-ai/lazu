export type WalletLedgerDay = {
  closing_balance_microusd?: unknown;
  credit_microusd?: unknown;
  date?: unknown;
  spend_microusd?: unknown;
};

export type WalletLedgerRow = {
  balance: number;
  credit: number;
  date: string;
  id: string;
  spend: number;
};

export type WalletTab = 'topup' | 'billing';
export type WalletBillingView = 'daily' | 'orders';

type WalletStatement = {
  opening: number;
  credits: number;
  spend: number;
  closing: number;
};

const finiteNumber = (value: unknown) => {
  const number = Number(value);
  return Number.isFinite(number) ? number : 0;
};

export const buildDailyWalletLedger = (
  records: WalletLedgerDay[],
  microusdPerUsd = 1_000_000,
): WalletLedgerRow[] =>
  records
    .map((record) => ({
      balance: finiteNumber(record.closing_balance_microusd) / microusdPerUsd,
      credit: finiteNumber(record.credit_microusd) / microusdPerUsd,
      date: String(record.date || ''),
      id: `ledger-${String(record.date || '')}`,
      spend: finiteNumber(record.spend_microusd) / microusdPerUsd,
    }))
    .filter((record) => /^\d{4}-\d{2}-\d{2}$/.test(record.date))
    .sort((left, right) => (left.date < right.date ? 1 : -1));

export const reconcileWalletStatement = ({
  openingBalanceMicrousd,
  ledgerRows,
  period,
  liveWalletBalanceUSD,
  microusdPerUsd = 1_000_000,
}: {
  openingBalanceMicrousd: number | null | undefined;
  ledgerRows: WalletLedgerRow[];
  period: 'current' | 'previous';
  liveWalletBalanceUSD: number | null;
  microusdPerUsd?: number;
}): WalletStatement | null => {
  if (
    openingBalanceMicrousd === null ||
    openingBalanceMicrousd === undefined ||
    !Number.isFinite(Number(openingBalanceMicrousd))
  ) {
    return null;
  }

  const openingCents = Math.round(
    (Number(openingBalanceMicrousd) / microusdPerUsd) * 100,
  );
  let creditsCents = Math.max(
    0,
    Math.round(ledgerRows.reduce((sum, row) => sum + row.credit, 0) * 100),
  );
  const rawSpendCents = Math.max(
    0,
    Math.round(ledgerRows.reduce((sum, row) => sum + row.spend, 0) * 100),
  );

  const targetClosingUSD =
    period === 'current' &&
    liveWalletBalanceUSD !== null &&
    Number.isFinite(liveWalletBalanceUSD)
      ? liveWalletBalanceUSD
      : (ledgerRows[0]?.balance ??
        Number(openingBalanceMicrousd) / microusdPerUsd);
  const closingCents = Math.round(targetClosingUSD * 100);

  const impliedSpendCents = openingCents + creditsCents - closingCents;
  let spendCents: number;
  if (impliedSpendCents >= 0) {
    spendCents = impliedSpendCents;
  } else {
    spendCents = rawSpendCents;
    creditsCents = Math.max(0, closingCents + spendCents - openingCents);
  }

  return {
    opening: openingCents / 100,
    credits: creditsCents / 100,
    spend: spendCents / 100,
    closing: closingCents / 100,
  };
};

export const resolveWalletRouteState = (
  searchParams: URLSearchParams,
  isRootUser: boolean,
): {
  tab: WalletTab;
  billingView: WalletBillingView;
  ownerParam: string;
} => {
  const ownerParam = isRootUser ? searchParams.get('owner')?.trim() || '' : '';
  const recordId = searchParams.get('top_up_id')?.trim() || '';
  const rawTab = searchParams.get('tab')?.trim() || '';
  const rawView = searchParams.get('view')?.trim() || '';

  if (rawTab === 'topup') {
    return { tab: 'topup', billingView: 'daily', ownerParam };
  }
  if (rawTab === 'orders') {
    return { tab: 'billing', billingView: 'orders', ownerParam };
  }
  if (rawTab === 'billing') {
    return {
      tab: 'billing',
      billingView:
        rawView === 'daily'
          ? 'daily'
          : rawView === 'orders' || Boolean(ownerParam) || Boolean(recordId)
            ? 'orders'
            : 'daily',
      ownerParam,
    };
  }
  if (ownerParam || rawView === 'orders') {
    return { tab: 'billing', billingView: 'orders', ownerParam };
  }
  return { tab: 'topup', billingView: 'daily', ownerParam };
};

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

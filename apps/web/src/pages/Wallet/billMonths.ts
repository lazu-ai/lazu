/* The bill tab's arithmetic, kept apart from the page so it can be tested:
 * which months the bill covers, the half-open range each is asked for, and
 * the month summary built from one ledger response. */
import dayjs, { type Dayjs } from 'dayjs';
import {
  buildDailyWalletLedger,
  type WalletLedgerDay,
  type WalletLedgerRow,
} from './walletLedger';

export type WalletLedgerRange = {
  items?: WalletLedgerDay[];
  opening_balance_microusd?: number;
};

export type WalletMonth = {
  closing: number;
  credit: number;
  /* Days with a movement, newest first. */
  days: WalletLedgerRow[];
  key: string;
  opening: number;
  spend: number;
};

/** How many months the bill shows, the current one included. */
const BILL_MONTHS = 6;

const MONTH_KEY = /^\d{4}-\d{2}$/;
const DAY_KEY = /^\d{4}-\d{2}-\d{2}$/;

export const isMonthKey = (value: string | null | undefined): value is string =>
  Boolean(value && MONTH_KEY.test(value) && dayjs(`${value}-01`).isValid());

export const isDayKey = (value: string | null | undefined): value is string =>
  Boolean(value && DAY_KEY.test(value) && dayjs(value).isValid());

/** The bill's months, newest first: the current month and the five before. */
export const billMonthKeys = (now: Dayjs, count = BILL_MONTHS) =>
  Array.from({ length: count }, (_, index) =>
    now.startOf('month').subtract(index, 'month').format('YYYY-MM'),
  );

/** The half-open [start, end) Unix range one month is read with. The
 *  current month stops one second past the end of today, so the newest
 *  movements of the day are included. */
export const monthRange = (key: string, now: Dayjs) => {
  const start = dayjs(`${key}-01`).startOf('month');
  const isCurrent = start.isSame(now, 'month');
  const end = isCurrent
    ? now.endOf('day').add(1, 'second')
    : start.add(1, 'month');
  return { end: end.unix(), isCurrent, start: start.unix() };
};

/** The [start, end] range of one calendar day, as the usage endpoints take it. */
export const dayRange = (key: string) => {
  const day = dayjs(key);
  return { end: day.endOf('day').unix(), start: day.startOf('day').unix() };
};

const finite = (value: unknown) => {
  const number = Number(value);
  return Number.isFinite(number) ? number : 0;
};

export const summarizeMonth = (
  key: string,
  data: WalletLedgerRange | undefined,
  microusdPerUsd = 1_000_000,
): WalletMonth => {
  const days = buildDailyWalletLedger(data?.items || [], microusdPerUsd);
  const opening = finite(data?.opening_balance_microusd) / microusdPerUsd;
  return {
    closing: days[0]?.balance ?? opening,
    credit: days.reduce((sum, day) => sum + day.credit, 0),
    days,
    key,
    opening,
    spend: days.reduce((sum, day) => sum + day.spend, 0),
  };
};

/** Bar heights for the six-month chart, as a percentage of the bar area: the
 *  largest month fills 82% so its value label fits above it, and a month
 *  with nothing still shows a 2% sliver so the slot reads as a month. */
export const monthBarHeight = (value: number, max: number) =>
  max > 0 ? Math.max(2, (value / max) * 82) : 2;

/** Bar heights for a month's daily chart: the largest day fills the area. */
export const dayBarHeight = (value: number, max: number) =>
  max > 0 ? Math.max(3, (value / max) * 100) : 3;

const csvCell = (value: string) =>
  /[",\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value;

export const walletCsv = (
  header: string[],
  rows: Array<Array<string | number>>,
) =>
  [header, ...rows]
    .map((row) => row.map((cell) => csvCell(String(cell))).join(','))
    .join('\n');

export const downloadCsv = (name: string, body: string) => {
  const blob = new Blob([body], { type: 'text/csv;charset=utf-8;' });
  const link = document.createElement('a');
  link.href = URL.createObjectURL(blob);
  link.download = name;
  link.click();
  URL.revokeObjectURL(link.href);
};

/* Presentation helpers for contract money. Everything the API returns is
 * microUSD; the console only ever shows USD. */

export const MICRO = 1_000_000;

export const toUSD = (micro: number | null | undefined): number | null =>
  micro === null || micro === undefined || !Number.isFinite(Number(micro))
    ? null
    : Number(micro) / MICRO;

export const toMicro = (usd: number): number => Math.round(usd * MICRO);

export const formatUSD = (
  micro: number | null | undefined,
  options: { unlimited?: string; digits?: number } = {},
): string => {
  const usd = toUSD(micro);
  if (usd === null) return options.unlimited ?? '—';
  const digits = options.digits ?? 2;
  return `$${usd.toLocaleString('en-US', {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })}`;
};

/** Short form for chips and quick presets: $50, $1,000, $12.50 */
export const formatUSDShort = (micro: number | null | undefined): string => {
  const usd = toUSD(micro);
  if (usd === null) return '—';
  const whole = Number.isInteger(usd);
  return `$${usd.toLocaleString('en-US', {
    minimumFractionDigits: whole ? 0 : 2,
    maximumFractionDigits: 2,
  })}`;
};

export const percentOf = (
  spent: number,
  cap: number | null | undefined,
): number | null => {
  if (cap === null || cap === undefined || cap <= 0) return null;
  return Math.min(999, Math.round((spent / cap) * 100));
};

export const budgetTone = (
  spent: number,
  cap: number | null | undefined,
  warningPct = 80,
): 'ok' | 'warn' | 'bad' => {
  const pct = percentOf(spent, cap);
  if (pct === null) return 'ok';
  if (pct >= 100) return 'bad';
  if (pct >= warningPct) return 'warn';
  return 'ok';
};

export const daysUntil = (unix: number, now = Date.now()): number =>
  Math.max(0, Math.ceil((unix * 1000 - now) / 86_400_000));

/** Budgets reset on the first of the next month in UTC, in every locale. */
export const formatBudgetResetDate = (
  locale: string,
  now = new Date(),
): string =>
  new Intl.DateTimeFormat(locale, {
    month: 'long',
    day: 'numeric',
    timeZone: 'UTC',
  }).format(new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + 1, 1)));

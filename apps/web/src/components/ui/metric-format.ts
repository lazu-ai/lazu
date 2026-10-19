/**
 * Number formatting for console metrics. Counts switch to k / M / B as soon
 * as they pass a thousand so a figure never reads as a wall of digits; money
 * keeps cents below $1k and compacts above it only where space is tight.
 */

function trimZeros(text: string): string {
  return text.includes('.') ? text.replace(/0+$/, '').replace(/\.$/, '') : text;
}

function significant(value: number): string {
  if (value >= 100) return trimZeros(value.toFixed(0));
  if (value >= 10) return trimZeros(value.toFixed(1));
  return trimZeros(value.toFixed(2));
}

/** 1,234 → 1.23k, 21_600_000 → 21.6M. */
export function formatCount(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return '—';
  const abs = Math.abs(value);
  const sign = value < 0 ? '−' : '';
  if (abs >= 1e9) return `${sign}${significant(abs / 1e9)}B`;
  if (abs >= 1e6) return `${sign}${significant(abs / 1e6)}M`;
  if (abs >= 1e3) return `${sign}${significant(abs / 1e3)}k`;
  return `${sign}${Math.round(abs)}`;
}

/** microUSD → "$1,234.56". */
export function formatMicroUsd(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return '—';
  const usd = value / 1_000_000;
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 2,
    maximumFractionDigits: Math.abs(usd) < 0.1 && usd !== 0 ? 4 : 2,
  }).format(usd);
}

/** microUSD → "$1.2k" for axes and dense cells. */
export function formatMicroUsdCompact(
  value: number | null | undefined,
): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return '—';
  const usd = value / 1_000_000;
  if (Math.abs(usd) >= 1000) return `$${significant(usd / 1000)}k`;
  if (Math.abs(usd) >= 100) return `$${usd.toFixed(0)}`;
  return `$${usd.toFixed(2)}`;
}

/** 0.9912 → "99.12%". */
export function formatRatio(
  value: number | null | undefined,
  digits = 2,
): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return '—';
  return `${(value * 100).toFixed(digits)}%`;
}

/** Seconds → "46 分钟" / "2 小时 5 分钟" style, localised by the caller's t. */
export function splitDuration(seconds: number): {
  hours: number;
  minutes: number;
} {
  const safe = Math.max(0, Math.round(seconds));
  return {
    hours: Math.floor(safe / 3600),
    minutes: Math.round((safe % 3600) / 60),
  };
}

/** Relative change between two totals; null when there is no baseline. */
export function relativeChange(
  current: number,
  previous: number,
): number | null {
  if (!Number.isFinite(current) || !Number.isFinite(previous) || previous <= 0)
    return null;
  return (current - previous) / previous;
}

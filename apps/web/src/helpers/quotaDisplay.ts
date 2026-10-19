import { addMicroUSD, asMicroUSD, type MicroUSD } from './money';

type QuotaDisplayInput = {
  remaining: MicroUSD | number | string | null | undefined;
  unlimited?: boolean;
  used: MicroUSD | number | string | null | undefined;
};

type FiniteQuotaDisplayInput = QuotaDisplayInput & {
  unlimited?: false | null | undefined;
};

type UnlimitedQuotaDisplayInput = QuotaDisplayInput & {
  unlimited: true;
};

type FiniteQuotaDisplay = {
  mode: 'finite';
  remaining: MicroUSD;
  remainingPercent: number;
  total: MicroUSD;
  used: MicroUSD;
  usedPercent: number;
};

type UnlimitedQuotaDisplay = {
  mode: 'unlimited';
  remaining: MicroUSD;
  total: null;
  used: MicroUSD;
};

export type QuotaDisplay = FiniteQuotaDisplay | UnlimitedQuotaDisplay;

function calculateUsedPercent(used: MicroUSD, total: MicroUSD) {
  if (total <= 0) {
    return used > 0 ? 100 : 0;
  }

  return Math.min(100, Math.max(0, (used / total) * 100));
}

function calculateRemainingPercent(remaining: MicroUSD, total: MicroUSD) {
  if (total <= 0) {
    return 0;
  }

  return Math.min(100, Math.max(0, (remaining / total) * 100));
}

export function buildQuotaDisplay(
  input: FiniteQuotaDisplayInput,
): FiniteQuotaDisplay;
export function buildQuotaDisplay(
  input: UnlimitedQuotaDisplayInput,
): UnlimitedQuotaDisplay;
export function buildQuotaDisplay(input: QuotaDisplayInput): QuotaDisplay;
export function buildQuotaDisplay({
  remaining,
  unlimited = false,
  used,
}: QuotaDisplayInput): QuotaDisplay {
  const remainingQuota = asMicroUSD(remaining);
  const usedQuota = asMicroUSD(used);

  if (unlimited) {
    return {
      mode: 'unlimited',
      remaining: remainingQuota,
      total: null,
      used: usedQuota,
    };
  }

  const totalQuota = addMicroUSD(remainingQuota, usedQuota);

  return {
    mode: 'finite',
    remaining: remainingQuota,
    remainingPercent: calculateRemainingPercent(remainingQuota, totalQuota),
    total: totalQuota,
    used: usedQuota,
    usedPercent: calculateUsedPercent(usedQuota, totalQuota),
  };
}

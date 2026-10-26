declare const microUSDBrand: unique symbol;

export type MicroUSD = number & {
  readonly [microUSDBrand]: true;
};

export function asMicroUSD(value: unknown): MicroUSD {
  const amount = Number(value ?? 0);
  return (Number.isFinite(amount) ? amount : 0) as MicroUSD;
}

export function addMicroUSD(...values: MicroUSD[]): MicroUSD {
  return asMicroUSD(values.reduce((total, value) => total + value, 0));
}

export function negateMicroUSD(value: MicroUSD): MicroUSD {
  return asMicroUSD(-value);
}

export function absMicroUSD(value: MicroUSD): MicroUSD {
  return asMicroUSD(Math.abs(value));
}

// The site's conversion rate, which /api/status writes to localStorage. A
// missing or unusable value falls back to 1,000,000 microUSD per dollar.
export function getMicroUSDPerUSD() {
  const microUSDPerUSD = parseFloat(
    localStorage.getItem('micro_usd_per_usd') || '1000000',
  );
  return Number.isFinite(microUSDPerUSD) && microUSDPerUSD > 0
    ? microUSDPerUSD
    : 1000000;
}

export function usdToMicroUSD(amount: number | string): MicroUSD {
  const dollars = Number(amount);
  return asMicroUSD(
    Math.round((Number.isFinite(dollars) ? dollars : 0) * getMicroUSDPerUSD()),
  );
}

export function microUSDToUSD(amountMicrousd: MicroUSD): number {
  return amountMicrousd / getMicroUSDPerUSD();
}

// A number with up to `digits` decimals and no trailing zeros.
export function formatDecimalValue(value: unknown, digits = 6) {
  if (value === undefined || value === null || Number.isNaN(value)) {
    return '0';
  }
  const fixed = Number(value).toFixed(digits);
  if (!fixed.includes('.')) {
    return fixed;
  }
  let trimmed = fixed.replace(/0+$/, '');
  if (trimmed.endsWith('.')) {
    trimmed = trimmed.slice(0, -1);
  }
  if (trimmed === '' || trimmed === '-0') {
    return '0';
  }
  return trimmed;
}

// A microUSD amount as dollars, with up to `digits` decimals. A positive
// amount too small to show at that precision reads as the smallest step
// ($0.01 at two digits) rather than $0, so a charge never looks free.
export function formatMoney(
  amount: MicroUSD | null | undefined,
  digits = 2,
): string {
  const microusd = asMicroUSD(amount);
  const dollars = microUSDToUSD(microusd);
  const fixed = formatDecimalValue(dollars, digits);
  if (parseFloat(fixed) === 0 && microusd > 0 && dollars > 0) {
    return '$' + formatDecimalValue(Math.pow(10, -digits), digits);
  }
  return '$' + fixed;
}

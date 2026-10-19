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

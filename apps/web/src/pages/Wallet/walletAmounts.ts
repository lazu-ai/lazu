export type TopupAmountRecord = {
  amount?: unknown;
  complete_time?: unknown;
  credited_microusd?: unknown;
  create_time?: unknown;
  paid_amount?: unknown;
  status?: unknown;
};

const finiteNumber = (value: unknown) => {
  const number = Number(value);
  return Number.isFinite(number) ? number : null;
};

export const resolveTopupCreditUSD = (
  record: TopupAmountRecord,
  microUSDPerUSD = 1_000_000,
) => {
  const creditedMicrousd = finiteNumber(record.credited_microusd);
  if (creditedMicrousd !== null && creditedMicrousd > 0) {
    return creditedMicrousd / microUSDPerUSD;
  }

  // Orders are priced in whole dollars; an unsettled one credits its amount.
  const orderUSD = finiteNumber(record.amount);
  return orderUSD !== null && orderUSD > 0 ? orderUSD : 0;
};

export const resolveTopupPaidUSD = (record: TopupAmountRecord) => {
  const paidUSD = finiteNumber(record.paid_amount);
  if (paidUSD !== null && paidUSD > 0) return paidUSD;

  const legacyPaidUSD = finiteNumber(record.amount);
  return legacyPaidUSD !== null && legacyPaidUSD > 0 ? legacyPaidUSD : 0;
};

export const resolveTopupEffectiveTimestamp = (record: TopupAmountRecord) => {
  if (String(record.status || '') === 'success') {
    const completed = finiteNumber(record.complete_time);
    if (completed !== null && completed > 0) return completed;
  }

  const created = finiteNumber(record.create_time);
  return created !== null && created > 0 ? created : 0;
};

import { useQuery } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { useMemo } from 'react';
import { tokenStatusCode } from './tokenStatusCode';
import { asMicroUSD } from '../../helpers/money';
import {
  fetchAllTokenRecords,
  type TokenRecord,
} from '../../hooks/tokens/useTokensQuery';
import { queryKeys } from '../../lib/query-keys';

const DAY = 24 * 60 * 60;
/** "Almost out" starts where the usage alert's default threshold does. */
const NEARLY_OUT = 0.8;
const EXPIRING_DAYS = 7;

/** Seconds until a Key expires, or null when it never does. */
export const secondsToExpiry = (record: TokenRecord, now = dayjs().unix()) => {
  const expires = Number(record.expired_time);
  if (!Number.isFinite(expires) || expires === -1 || expires <= 0) return null;
  return expires - now;
};

/** A live Key that has spent most of a finite budget. */
export const isNearlyOut = (record: TokenRecord) => {
  if (record.unlimited_quota) return false;
  const used = Number(asMicroUSD(record.used_quota)) || 0;
  const remaining = Number(asMicroUSD(record.remain_quota)) || 0;
  const total = used + remaining;
  return total > 0 && used / total >= NEARLY_OUT;
};

export type TokenOverview = {
  /** Keys per status code, plus `all`. */
  counts: Record<'all' | '1' | '2' | '3' | '4', number>;
  enabled: number;
  expiringSoon: number;
  nearlyOut: number;
};

const summarize = (records: TokenRecord[]): TokenOverview => {
  const counts = { all: records.length, '1': 0, '2': 0, '3': 0, '4': 0 };
  let expiringSoon = 0;
  let nearlyOut = 0;
  const now = dayjs().unix();
  records.forEach((record) => {
    const code = String(tokenStatusCode(record)) as keyof typeof counts;
    if (code in counts && code !== 'all') counts[code] += 1;
    if (code !== '1') return;
    const left = secondsToExpiry(record, now);
    if (left !== null && left > 0 && left <= EXPIRING_DAYS * DAY) {
      expiringSoon += 1;
    }
    if (isNearlyOut(record)) nearlyOut += 1;
  });
  return { counts, enabled: counts['1'], expiringSoon, nearlyOut };
};

/** Live Keys that run out of time within a week. */
export const isExpiringSoon = (record: TokenRecord, now = dayjs().unix()) => {
  if (String(tokenStatusCode(record)) !== '1') return false;
  const left = secondsToExpiry(record, now);
  return left !== null && left > 0 && left <= EXPIRING_DAYS * DAY;
};

/** Live Keys past most of a finite budget. */
export const isNearlyOutLive = (record: TokenRecord) =>
  String(tokenStatusCode(record)) === '1' && isNearlyOut(record);

/** Counts for the status tabs, over every Key rather than the page on screen,
 *  plus the Keys themselves for the two attention tabs, which no server-side
 *  status describes. */
export function useTokenOverview() {
  const recordsQuery = useQuery({
    queryKey: queryKeys.console.detail('tokens', 'overview'),
    queryFn: fetchAllTokenRecords,
    staleTime: 30_000,
  });
  const overview = useMemo(
    () => (recordsQuery.data ? summarize(recordsQuery.data) : null),
    [recordsQuery.data],
  );
  return { overview, records: recordsQuery.data };
}

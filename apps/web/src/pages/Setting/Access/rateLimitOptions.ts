/* Account rate limits (速率限制): one site-wide default, a lower ceiling for
 * accounts that have never topped up, and per-account overrides set on the
 * account drawer. 0 means no limit at every level; all of it applies only while
 * ModelRequestRateLimitEnabled is on (internal/setting/rate_limit.go). */
import { useQuery } from '@tanstack/react-query';
import { API } from '../../../helpers';
import { unwrap } from '../../../lib/api-error';
import { queryKeys } from '../../../lib/query-keys';

export type RateLimitOptions = {
  defaultRpm: number;
  defaultTpm: number;
  enabled: boolean;
  unpaidRpm: number;
  unpaidTpm: number;
};

export const RATE_LIMIT_OPTION_KEYS = {
  defaultRpm: 'RateLimitDefaultRPM',
  defaultTpm: 'RateLimitDefaultTPM',
  enabled: 'ModelRequestRateLimitEnabled',
  unpaidRpm: 'RateLimitUnpaidRPM',
  unpaidTpm: 'RateLimitUnpaidTPM',
} as const satisfies Record<keyof RateLimitOptions, string>;

export const RATE_LIMIT_QUERY_KEY = queryKeys.console.detail(
  'settings-rate-limits',
  'options',
);

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === 'object' && !Array.isArray(value);

const count = (value: unknown) => {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : 0;
};

export const parseRateLimitOptions = (items: unknown): RateLimitOptions => {
  const map = new Map<string, unknown>();
  if (Array.isArray(items)) {
    items
      .filter(isRecord)
      .forEach((item) => map.set(String(item.key), item.value));
  }
  return {
    defaultRpm: count(map.get(RATE_LIMIT_OPTION_KEYS.defaultRpm)),
    defaultTpm: count(map.get(RATE_LIMIT_OPTION_KEYS.defaultTpm)),
    enabled: String(map.get(RATE_LIMIT_OPTION_KEYS.enabled)) === 'true',
    unpaidRpm: count(map.get(RATE_LIMIT_OPTION_KEYS.unpaidRpm)),
    unpaidTpm: count(map.get(RATE_LIMIT_OPTION_KEYS.unpaidTpm)),
  };
};

export function useRateLimitOptionsQuery() {
  return useQuery({
    queryKey: RATE_LIMIT_QUERY_KEY,
    refetchOnWindowFocus: false,
    queryFn: async () =>
      parseRateLimitOptions(unwrap<unknown>(await API.get('/api/option/'))),
  });
}

/** 60 → "60", 100000 → "100,000", 0 → the caller's "不限". */
export const formatLimit = (value: number, unlimited: string) =>
  value > 0 ? value.toLocaleString('en-US') : unlimited;

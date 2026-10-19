import type { TokenRecord } from '../../hooks/tokens/useTokensQuery';

/** The project token payload types `status` as a string (enabled | disabled |
 *  expired | exhausted) and carries the legacy integer beside it as
 *  `status_code`. This page speaks the integer, so resolve whichever the
 *  server sent: `status_code` first, then a numeric `status`, then the string.
 *  Returns 0 when the record carries no readable status. */
export const tokenStatusCode = (record: TokenRecord | unknown): number => {
  const row = (record ?? {}) as Record<string, unknown>;
  if (typeof row.status_code === 'number') return row.status_code;
  if (typeof row.status === 'number') return row.status;
  const named: Record<string, number> = {
    enabled: 1,
    disabled: 2,
    expired: 3,
    exhausted: 4,
  };
  if (typeof row.status === 'string' && row.status in named) {
    return named[row.status];
  }
  return 0;
};

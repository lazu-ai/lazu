import type { TokenRecord } from '../../hooks/tokens/useTokensQuery';

/** The list payload masks a Key as its prefix and last four characters
 *  ("sk-lazu-972y...XlMg"); older rows only carry the tail. */
export const shortKeyLabel = (record: TokenRecord) => {
  const masked = String(record.masked_key || '').trim();
  if (masked.includes('...')) return masked.replace('...', '…');
  const tail = masked.replace(/[^A-Za-z0-9]/g, '').slice(-4);
  return tail ? `sk-…${tail}` : 'sk-…';
};

/** Whether this Key's value is gone for good (stored only as a digest). */
export const isLegacyKey = (record: TokenRecord) =>
  record.secret_available === false ||
  (record.secret_available === undefined && record.can_reveal_key === false);

import { safeJsonParse } from './utils';

const parseJson = safeJsonParse as (
  value: unknown,
  fallback: unknown,
) => unknown;

export function getLogOther(otherStr?: string): Record<string, unknown> {
  const normalizedOther =
    otherStr === undefined || otherStr === '' ? '{}' : otherStr;
  const other = parseJson(normalizedOther, {});
  if (other && typeof other === 'object' && !Array.isArray(other)) {
    return other as Record<string, unknown>;
  }
  return {};
}

/* A source's request overrides: param_override (JSON merged into, or
   operations applied to, the upstream request body), header_override (header
   name to value, `{api_key}` replaced by the key) and status_code_mapping
   (upstream status to the status the client sees). The relay applies all
   three; the form edits them as JSON text. */

export const STATUS_CODE_MAPPING_MAX_LENGTH = 1024; // the column's width

const STATUS_CODE = /^[1-5]\d\d$/;

function parseObject(text: string): Record<string, unknown> | null {
  try {
    const value = JSON.parse(text) as unknown;
    return value && typeof value === 'object' && !Array.isArray(value)
      ? (value as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
}

export function isParamOverride(text: string | undefined): boolean {
  return !text?.trim() || parseObject(text) !== null;
}

export function isHeaderOverride(text: string | undefined): boolean {
  if (!text?.trim()) return true;
  const parsed = parseObject(text);
  return (
    parsed !== null &&
    Object.entries(parsed).every(
      ([name, value]) => name.trim() !== '' && typeof value === 'string',
    )
  );
}

export function isStatusCodeMapping(text: string | undefined): boolean {
  if (!text?.trim()) return true;
  const parsed = parseObject(text);
  return (
    parsed !== null &&
    Object.entries(parsed).every(
      ([from, to]) =>
        STATUS_CODE.test(from) &&
        typeof to === 'string' &&
        STATUS_CODE.test(to),
    )
  );
}

export function fitsStatusCodeMapping(text: string | undefined): boolean {
  return compactOverride(text).length <= STATUS_CODE_MAPPING_MAX_LENGTH;
}

/** The stored value as the form shows it: pretty-printed when it is JSON. */
export function overrideFormText(stored: unknown): string {
  const text = typeof stored === 'string' ? stored.trim() : '';
  if (!text) return '';
  const parsed = parseObject(text);
  return parsed && Object.keys(parsed).length
    ? JSON.stringify(parsed, null, 2)
    : parsed
      ? ''
      : text;
}

/** The form text as it is sent: compact JSON, or "" to clear the field. */
export function compactOverride(text: string | undefined): string {
  const trimmed = String(text ?? '').trim();
  if (!trimmed) return '';
  const parsed = parseObject(trimmed);
  if (!parsed) return trimmed;
  return Object.keys(parsed).length ? JSON.stringify(parsed) : '';
}

/* The sentence a settings field shows when the server refused its value.
 *
 * The server answers every refused value with one code, `setting_invalid`,
 * and says what went wrong in details: `field` is the option key, `rule` is
 * the rule the value broke, and list settings add a 1-based `index`, the
 * item's attribute `item`, and a `limit` where one applies. The toast reads
 * the code's generic copy; this turns the details into the precise sentence
 * that belongs under the field. */
import { readApiError } from '../../lib/api-error';
import type { TFunction } from 'i18next';

export type SettingInvalid = {
  /** The option key the server named, e.g. `EmailDomainWhitelist`. */
  field: string;
  /** A localized sentence for the field. */
  message: string;
};

const RULES = new Set([
  'requires_values',
  'locked_while_enabled',
  'min_integer',
  'integer_range',
  'format',
  'required',
  'too_long',
  'not_allowed',
  'duplicate',
  'url',
  'slug_chars',
  'unsafe_content',
]);

const asNumber = (value: unknown): number | undefined =>
  typeof value === 'number' && Number.isFinite(value) ? value : undefined;

export function readSettingInvalid(
  error: unknown,
  t: TFunction,
): SettingInvalid | null {
  const info = readApiError(error);
  if (info.code !== 'setting_invalid') return null;
  const field =
    typeof info.details.field === 'string' ? info.details.field : '';
  if (!field) return null;
  const rule =
    typeof info.details.rule === 'string' && RULES.has(info.details.rule)
      ? info.details.rule
      : 'format';
  const limit = asNumber(info.details.limit);
  const index = asNumber(info.details.index);
  const problem = t(`page.console.settingV2.runtime.invalid.${rule}`, {
    limit,
    min: asNumber(info.details.min),
    max: asNumber(info.details.max),
  });
  const message =
    index === undefined
      ? problem
      : t('page.console.settingV2.runtime.invalid.item', { index, problem });
  return { field, message };
}

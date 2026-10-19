/* Budget input rules shared by the inline editor, the invite dialog, the
 * member detail save bar and the settings page. */
import { MICRO } from './money';

export const BUDGET_MIN_USD = 1;
export const BUDGET_MAX_USD = 100_000;
export const QUICK_BUDGETS_USD = [50, 100, 500] as const;
export const DEFAULT_MEMBER_BUDGET_MICRO = 100 * MICRO;
export const DEFAULT_WARNING_PCT = 80;
export const MAX_PERSONAL_KEYS = 10;
export const INVITE_VALID_DAYS = 3;
export const REVEAL_REMASK_SECONDS = 30;
export const REMOVAL_UNDO_MINUTES = 10;
export const RECEIPT_SECONDS = 6;
export const BUDGET_REQUEST_NOTE_MIN = 4;

export type BudgetDraft =
  | { mode: 'unlimited' }
  | { mode: 'amount'; input: string };

export type BudgetValidation =
  | { ok: true; micro: number | null }
  | { ok: false; error: 'invalid' | 'below_min' | 'above_max' | 'empty' };

/** "0" is not "unlimited" — unlimited is only ever the explicit choice. */
export function validateBudget(draft: BudgetDraft): BudgetValidation {
  if (draft.mode === 'unlimited') return { ok: true, micro: null };
  const raw = draft.input.trim().replace(/[$,\s]/g, '');
  if (!raw) return { ok: false, error: 'empty' };
  if (!/^\d+(\.\d{0,2})?$/.test(raw)) return { ok: false, error: 'invalid' };
  const usd = Number(raw);
  if (!Number.isFinite(usd)) return { ok: false, error: 'invalid' };
  if (usd !== 0 && usd < BUDGET_MIN_USD)
    return { ok: false, error: 'below_min' };
  if (usd > BUDGET_MAX_USD) return { ok: false, error: 'above_max' };
  return { ok: true, micro: Math.round(usd * MICRO) };
}

/** Lowering a cap under month-to-date spend is allowed but must be said. */
export const lowersBelowSpent = (
  nextMicro: number | null,
  spentMicro: number,
): boolean => nextMicro !== null && nextMicro < spentMicro;

export const draftFromMicro = (micro: number | null): BudgetDraft =>
  micro === null
    ? { mode: 'unlimited' }
    : { mode: 'amount', input: (micro / MICRO).toFixed(2) };

export const draftEquals = (a: BudgetDraft, b: BudgetDraft): boolean => {
  const va = validateBudget(a);
  const vb = validateBudget(b);
  if (va.ok && vb.ok) return va.micro === vb.micro;
  return (
    a.mode === b.mode &&
    (a.mode === 'unlimited' || a.input === (b as { input: string }).input)
  );
};

export const validateWarningPct = (value: number): boolean =>
  Number.isInteger(value) && value >= 1 && value <= 99;

export const validateBudgetRequestNote = (note: string): boolean =>
  note.trim().length >= BUDGET_REQUEST_NOTE_MIN;

/** Top-up amounts come from the deployment's Stripe limits, not from here. */
export const validateTopUpAmount = (
  usd: number,
  min: number,
  max: number,
): 'ok' | 'below_min' | 'above_max' | 'invalid' => {
  if (!Number.isSafeInteger(usd) || usd <= 0) return 'invalid';
  if (usd < min) return 'below_min';
  if (usd > max) return 'above_max';
  return 'ok';
};

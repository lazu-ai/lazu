export type {
  Tone,
  LedgerItem,
} from '../../../components/ui/project-primitives';
export {
  Card,
  SectionHead,
  Ledger,
  Kvs,
  FormRows,
  FormRow,
  Avatar,
  Who,
  PjBadge,
  Meter,
  Bars,
  SegControl,
  OkLine,
  ErrLine,
  ConfirmPop,
  GateModal,
  ActionModal,
  SaveBar,
  Notice,
  TableFoot,
  SelectLike,
  ToggleGlyph,
  MonoText,
} from '../../../components/ui/project-primitives';
/* Project budget and reason adapters over shared Console primitives. */
import React, { useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Checkbox } from '../../../components/ui/checkbox';
import { Input } from '../../../components/ui/input';
import { PjBadge, Meter } from '../../../components/ui/project-primitives';
import { cn } from '../../../lib/cn';
import {
  BUDGET_MAX_USD,
  BUDGET_MIN_USD,
  QUICK_BUDGETS_USD,
  validateBudget,
  type BudgetDraft,
} from '../budget';
import { formatUSD, formatUSDShort, MICRO, percentOf } from '../money';
import { rejectionLabelKey, rejectionTone } from '../rejections';
import type { ProjectToken } from '../types';
import '../projects.css';

export function RoleBadge({ role }: { role: 'owner' | 'admin' | 'member' }) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  return (
    <PjBadge tone={role === 'member' ? 'neutral' : 'evi'}>
      {t(`projects.role.${role}`)}
    </PjBadge>
  );
}

/** A rejection reason chip: fixed copy per code, click = usage filter. */
export function ReasonChip({
  code,
  count,
  href,
  compact = false,
}: {
  code: string;
  count?: number;
  href?: string;
  compact?: boolean;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const label =
    count !== undefined
      ? t('projects.reason.chipCount', {
          count,
          reason: t(rejectionLabelKey(code)),
        })
      : t(rejectionLabelKey(code));
  const chip = (
    <span
      className='pj-rsn'
      data-tone={rejectionTone(code)}
      data-compact={compact || undefined}
    >
      {label}
    </span>
  );
  return href ? (
    <Link
      className='pj-rsn-link'
      to={href}
      title={t('projects.reason.openUsage')}
    >
      {chip}
    </Link>
  ) : (
    chip
  );
}

/** Which wall a Key hits first, exactly as the backend ranked it.
 *  Both the Keys page and a member's own overview render this, so the two
 *  never disagree about the same Key (the overview used to assume the
 *  member budget and ignore a lower per-Key cap). */
export function KeyLimitBadge({
  token,
  viewerId,
  projectSpentMicro = 0,
  projectCapMicro = null,
  as = 'badge',
}: {
  token: ProjectToken;
  viewerId: string;
  projectSpentMicro?: number;
  projectCapMicro?: number | null;
  /** "text" draws the wall as a line of words — the Keys table's form, where
   *  a row should not carry a row of filled chips. */
  as?: 'badge' | 'text';
}) {
  const Out = as === 'text' ? LimitText : BadgeOut;
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const { kind, remaining_microusd: remaining } = token.effective_limit;

  if (kind === 'key') {
    const pct = percentOf(token.month_spend_microusd, token.cap_microusd);
    return (
      <Out tone={pct !== null && pct >= 80 ? 'warn' : 'neutral'}>
        {t('projects.key.limitKey', {
          cap: formatUSDShort(token.cap_microusd),
          pct: pct ?? 0,
        })}
      </Out>
    );
  }

  if (kind === 'member') {
    const who =
      token.owner?.id === viewerId
        ? t('projects.key.myBudget')
        : t('projects.key.ownerBudget', { name: token.owner?.username ?? '' });
    if (remaining !== null && remaining <= 0)
      return <Out tone='bad'>{t('projects.key.limitUsedUp', { who })}</Out>;
    return (
      <Out
        tone={remaining !== null && remaining < 10 * MICRO ? 'warn' : 'neutral'}
      >
        {t('projects.key.limitLeft', {
          amount: formatUSDShort(remaining),
          who,
        })}
      </Out>
    );
  }

  if (kind === 'project')
    return (
      <Out>
        {t('projects.key.limitProject', {
          pct: percentOf(projectSpentMicro, projectCapMicro) ?? 0,
        })}
      </Out>
    );

  return (
    <Out>
      {t('projects.key.limitBalance', { amount: formatUSD(remaining) })}
    </Out>
  );
}

function BadgeOut(props: {
  tone?: 'neutral' | 'warn' | 'bad';
  children: React.ReactNode;
}) {
  return <PjBadge {...props} />;
}

function LimitText({
  tone = 'neutral',
  children,
}: {
  tone?: 'neutral' | 'warn' | 'bad';
  children: React.ReactNode;
}) {
  return (
    <span className='pj-limit' data-tone={tone}>
      {children}
    </span>
  );
}

/** Budget-history reasons arrive as enum strings (`invitation`, `manual`,
 *  `request:<id>`); render them as words rather than printing the raw value. */
export function budgetReasonText(
  reason: string,
  t: (key: string) => string,
): string {
  if (reason.startsWith('request:')) return t('projects.member.reason.request');
  if (reason === 'invitation' || reason === 'manual')
    return t(`projects.member.reason.${reason}`);
  return reason;
}

/* ---------------------------------------------------------------- meters */

export function BudgetCell({
  spent,
  cap,
  pct,
  tone,
}: {
  spent: number;
  cap: number | null;
  pct: number | null;
  tone: 'ok' | 'warn' | 'bad';
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  if (cap === null)
    return <span className='pj-muted'>{t('projects.budget.unlimited')}</span>;
  /* Figures in ink, a thin bar under them. Only a budget that is used up
     says so — in words, with the bar in the danger colour. */
  const out = tone === 'bad' && (pct ?? 0) >= 100;
  return (
    <div className='pj-quota pj-quota--cell'>
      <span className='pj-num pj-quota__fig'>
        {formatUSD(spent)} / {formatUSD(cap)}
        {out ? (
          <span className='pj-quota__out'> {t('projects.members.usedUp')}</span>
        ) : null}
      </span>
      <Meter className='pj-quota__meter' pct={pct} tone={out ? 'bad' : 'ok'} />
    </div>
  );
}

/** Seven small bars; the tallest is the peak. */
export function BudgetPicker({
  draft,
  onChange,
  quick = QUICK_BUDGETS_USD,
  allowUnlimited = true,
  disabled = false,
  size = 'md',
  focusOnMount = false,
  compact = false,
  label,
}: {
  draft: BudgetDraft;
  onChange: (draft: BudgetDraft) => void;
  quick?: readonly number[];
  allowUnlimited?: boolean;
  disabled?: boolean;
  size?: 'sm' | 'md';
  focusOnMount?: boolean;
  compact?: boolean;
  label?: string;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const [custom, setCustom] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (focusOnMount) inputRef.current?.focus();
  }, [focusOnMount]);
  const validation = validateBudget(draft);
  const currentMicro = validation.ok ? validation.micro : undefined;
  const matchesQuick = (usd: number) => currentMicro === usd * MICRO;
  const isCustom =
    custom || (draft.mode === 'amount' && !quick.some(matchesQuick));
  const invalid =
    draft.mode === 'amount' && !validation.ok && draft.input.trim() !== '';
  const inputId = useId();
  const lastAmount = useRef('100');
  if (draft.mode === 'amount') lastAmount.current = draft.input;
  if (compact)
    return (
      <div className='pj-inline pj-inline--gap'>
        <span aria-hidden='true'>$</span>
        <Input
          aria-label={label}
          aria-invalid={invalid || undefined}
          disabled={disabled || draft.mode === 'unlimited'}
          inputMode='decimal'
          value={draft.mode === 'amount' ? draft.input : lastAmount.current}
          onChange={(event) =>
            onChange({ mode: 'amount', input: event.target.value })
          }
          style={{ width: 110 }}
        />
        <label className='pj-inline pj-inline--gap'>
          <Checkbox
            disabled={disabled}
            checked={draft.mode === 'unlimited'}
            onCheckedChange={(checked) =>
              onChange(
                checked
                  ? { mode: 'unlimited' }
                  : { mode: 'amount', input: lastAmount.current },
              )
            }
          />
          {t('projects.budget.unlimited')}
        </label>
      </div>
    );

  return (
    <div className='pj-budget-picker' data-size={size}>
      {isCustom || draft.mode === 'amount' ? (
        <label
          className={cn('pj-amt', invalid && 'pj-amt--err')}
          htmlFor={inputId}
        >
          <span className='pj-amt__cur'>$</span>
          <input
            aria-invalid={invalid || undefined}
            ref={inputRef}
            disabled={disabled}
            id={inputId}
            inputMode='decimal'
            onChange={(event) =>
              onChange({ mode: 'amount', input: event.target.value })
            }
            onFocus={(event) => event.target.select()}
            value={draft.mode === 'amount' ? draft.input : ''}
          />
        </label>
      ) : null}
      <span className='pj-quick'>
        {quick.map((usd) => (
          <button
            className='pj-chip'
            data-on={matchesQuick(usd) || undefined}
            data-owns-chrome=''
            disabled={disabled}
            key={usd}
            onClick={() => {
              setCustom(false);
              onChange({ mode: 'amount', input: String(usd) });
            }}
            type='button'
          >
            {formatUSDShort(usd * MICRO)}
          </button>
        ))}
        <button
          className='pj-chip'
          data-on={(isCustom && draft.mode === 'amount') || undefined}
          data-owns-chrome=''
          disabled={disabled}
          onClick={() => {
            setCustom(true);
            if (draft.mode !== 'amount')
              onChange({ mode: 'amount', input: '' });
          }}
          type='button'
        >
          {t('projects.budget.custom')}
        </button>
        {allowUnlimited ? (
          <button
            className='pj-chip'
            data-on={draft.mode === 'unlimited' || undefined}
            data-owns-chrome=''
            disabled={disabled}
            onClick={() => {
              setCustom(false);
              onChange({ mode: 'unlimited' });
            }}
            type='button'
          >
            {t('projects.budget.unlimited')}
          </button>
        ) : null}
      </span>
    </div>
  );
}

export function budgetErrorText(
  draft: BudgetDraft,
  t: (key: string, opts?: Record<string, unknown>) => string,
): string {
  const validation = validateBudget(draft);
  if (validation.ok || draft.mode === 'unlimited') return '';
  if (validation.error === 'empty') return '';
  return t('projects.budget.rangeError', {
    min: BUDGET_MIN_USD,
    max: BUDGET_MAX_USD.toLocaleString('en-US'),
  });
}

export const formatDayTime = (unix: number | null | undefined) => {
  if (!unix) return '—';
  const d = new Date(unix * 1000);
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  const hh = String(d.getHours()).padStart(2, '0');
  const mi = String(d.getMinutes()).padStart(2, '0');
  return `${mm}-${dd} ${hh}:${mi}`;
};

export const formatDay = (unix: number | null | undefined) => {
  if (!unix) return '—';
  const d = new Date(unix * 1000);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
};

export const formatMonthDay = (unix: number | null | undefined) => {
  if (!unix) return '—';
  const d = new Date(unix * 1000);
  return `${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
};

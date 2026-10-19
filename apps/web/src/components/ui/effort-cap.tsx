import { useRef } from 'react';
import { cn } from '../../lib/cn';
import './effort-cap.css';

/* The reasoning-effort ladder, locked 2026-09-28 as direction A2 「填线」:
 * the six words the coding tools use sit in a row, and an ink line fills up to
 * the cap. The cap and every word below it are allowed; the words past it are
 * faint. A cap at "max" limits nothing and is stored as "". */
export const EFFORT_LEVELS = [
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
  'max',
] as const;
export type EffortLevel = (typeof EFFORT_LEVELS)[number];

/** Rung of a cap; "" (no cap) and unknown values read as max. */
export function effortRank(cap: string | null | undefined): number {
  const index = EFFORT_LEVELS.indexOf((cap ?? '') as EffortLevel);
  return index < 0 ? EFFORT_LEVELS.length - 1 : index;
}

/** The stored form of a cap: "" for max, the word otherwise. */
export function effortCapValue(level: EffortLevel): string {
  return level === 'max' ? '' : level;
}

/** The lower of two caps, either of which may be "" (no cap). */
export function lowerEffortCap(
  a: string | null | undefined,
  b: string | null | undefined,
): string {
  return effortCapValue(EFFORT_LEVELS[Math.min(effortRank(a), effortRank(b))]);
}

type EffortCapControlProps = {
  /** "" or a level word. */
  value: string;
  onChange?: (next: string) => void;
  /** Highest level that may be chosen, e.g. the member's cap for a Key. */
  ceiling?: string;
  size?: 'md' | 'lg';
  disabled?: boolean;
  'aria-label': string;
  className?: string;
};

export function EffortCapControl({
  value,
  onChange,
  ceiling,
  size = 'md',
  disabled = false,
  className,
  ...rest
}: EffortCapControlProps) {
  const rank = effortRank(value);
  const top = effortRank(ceiling);
  const refs = useRef<Array<HTMLButtonElement | null>>([]);
  const fill = ((Math.min(rank, top) + 0.5) / EFFORT_LEVELS.length) * 100;
  const pick = (index: number) => {
    if (disabled || index > top || !onChange) return;
    onChange(effortCapValue(EFFORT_LEVELS[index]));
  };
  return (
    <div
      aria-disabled={disabled || undefined}
      aria-label={rest['aria-label']}
      className={cn('lazu-effort', `lazu-effort--${size}`, className)}
      role='radiogroup'
    >
      <div className='lazu-effort__words'>
        {EFFORT_LEVELS.map((level, index) => {
          const checked = index === Math.min(rank, top);
          return (
            <button
              aria-checked={checked}
              className='lazu-effort__word'
              data-state={
                checked
                  ? 'cap'
                  : index > top
                    ? 'out'
                    : index < rank
                      ? 'on'
                      : 'off'
              }
              disabled={disabled || index > top}
              key={level}
              onClick={() => pick(index)}
              onKeyDown={(event) => {
                const step =
                  event.key === 'ArrowRight' || event.key === 'ArrowUp'
                    ? 1
                    : event.key === 'ArrowLeft' || event.key === 'ArrowDown'
                      ? -1
                      : 0;
                if (!step) return;
                event.preventDefault();
                const next = Math.max(0, Math.min(top, index + step));
                pick(next);
                refs.current[next]?.focus();
              }}
              ref={(node) => {
                refs.current[index] = node;
              }}
              role='radio'
              tabIndex={checked ? 0 : -1}
              type='button'
            >
              {level}
            </button>
          );
        })}
      </div>
      <div aria-hidden='true' className='lazu-effort__track'>
        <i className='lazu-effort__fill' style={{ width: `${fill}%` }} />
        <b className='lazu-effort__knob' style={{ left: `${fill}%` }} />
      </div>
    </div>
  );
}

/** A cap in a table cell: "≤ medium", or the all-levels label. */
export function EffortCapBadge({
  cap,
  allLabel,
  className,
}: {
  cap: string | null | undefined;
  allLabel: string;
  className?: string;
}) {
  const unlimited = effortRank(cap) === EFFORT_LEVELS.length - 1;
  return (
    <span
      className={cn('lazu-effort-badge', className)}
      data-all={unlimited || undefined}
    >
      {unlimited ? allLabel : `≤ ${cap}`}
    </span>
  );
}

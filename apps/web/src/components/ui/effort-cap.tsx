import { useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '../../lib/cn';
import type { KeyboardEvent, PointerEvent } from 'react';
import './effort-cap.css';

/* The reasoning-effort ladder. The cap is set on a track of six stops: click
 * anywhere on the track or its labels, drag the knob, or use the arrow keys.
 * The cap and every level below it are allowed; levels above a ceiling (the
 * member cap, for a Key) are faint and cannot be chosen. A cap at "max"
 * limits nothing and is stored as "". */
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

const LAST = EFFORT_LEVELS.length - 1;
const percentOf = (index: number) => (index / LAST) * 100;

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
  const { t } = useTranslation();
  const top = effortRank(ceiling);
  const current = Math.min(effortRank(value), top);
  const railRef = useRef<HTMLDivElement | null>(null);
  const knobRef = useRef<HTMLSpanElement | null>(null);
  const dragging = useRef(false);
  const readonly = disabled || !onChange;

  const labelOf = (index: number) =>
    t(`ui.effort.${EFFORT_LEVELS[index]}`, {
      defaultValue: EFFORT_LEVELS[index],
    });

  const pick = (index: number) => {
    if (disabled || !onChange) return;
    const next = Math.max(0, Math.min(top, index));
    if (next !== current) onChange(effortCapValue(EFFORT_LEVELS[next]));
  };

  const indexAt = (clientX: number) => {
    const box = railRef.current?.getBoundingClientRect();
    if (!box || box.width === 0) return current;
    return Math.round(((clientX - box.left) / box.width) * LAST);
  };

  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    if (readonly || event.button !== 0) return;
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    dragging.current = true;
    knobRef.current?.focus({ preventScroll: true });
    pick(indexAt(event.clientX));
  };
  const onPointerMove = (event: PointerEvent<HTMLDivElement>) => {
    if (dragging.current) pick(indexAt(event.clientX));
  };
  const stopDrag = () => {
    dragging.current = false;
  };

  const onKeyDown = (event: KeyboardEvent<HTMLSpanElement>) => {
    const steps: Record<string, number> = {
      ArrowRight: current + 1,
      ArrowUp: current + 1,
      ArrowLeft: current - 1,
      ArrowDown: current - 1,
      Home: 0,
      End: top,
    };
    if (!(event.key in steps)) return;
    event.preventDefault();
    pick(steps[event.key]);
  };

  const stateOf = (index: number) =>
    index === current
      ? 'cap'
      : index > top
        ? 'out'
        : index < current
          ? 'on'
          : 'off';

  return (
    <div
      aria-disabled={disabled || undefined}
      className={cn('lazu-effort', `lazu-effort--${size}`, className)}
      data-readonly={readonly || undefined}
      onLostPointerCapture={stopDrag}
      onPointerCancel={stopDrag}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={stopDrag}
    >
      <div className='lazu-effort__rail' ref={railRef}>
        <i
          className='lazu-effort__fill'
          style={{ width: `${percentOf(current)}%` }}
        />
        {EFFORT_LEVELS.map((level, index) => (
          <i
            className='lazu-effort__stop'
            data-level={level}
            data-state={stateOf(index)}
            key={level}
            style={{ left: `${percentOf(index)}%` }}
          />
        ))}
        <span
          aria-disabled={disabled || undefined}
          aria-label={rest['aria-label']}
          aria-valuemax={top}
          aria-valuemin={0}
          aria-valuenow={current}
          aria-valuetext={labelOf(current)}
          className='lazu-effort__knob'
          onKeyDown={onKeyDown}
          ref={knobRef}
          role='slider'
          style={{ left: `${percentOf(current)}%` }}
          tabIndex={disabled ? -1 : 0}
        />
      </div>
      <div aria-hidden='true' className='lazu-effort__words'>
        {EFFORT_LEVELS.map((level, index) => (
          <span
            className='lazu-effort__word'
            data-level={level}
            data-state={stateOf(index)}
            key={level}
            style={{ left: `${percentOf(index)}%` }}
          >
            {labelOf(index)}
          </span>
        ))}
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

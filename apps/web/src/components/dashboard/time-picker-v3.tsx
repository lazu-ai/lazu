/**
 * Lazu dashboard time picker. Ported from
 * the archived dashboard time-picker prototypes
 * with the project's conventions:
 *
 * - Typed throughout (DayKey / ResolvedRange / TimePickerValue).
 * - 5 presets per product spec: 24h / 3d / 7d (default) / 14d / 30d.
 *   3d + 14d were added on top of the design's 3.
 * - Custom range: first click sets start, second click sets end. Future
 *   days (after "today") are disabled — both visually and interactively.
 *   No double-click anywhere.
 * - Apply commits the draft; outside-click / Esc cancels.
 */
import React, {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

const ns = 'page.console.dashboard';

// ─── Date math (small, allocation-light) ─────────────────────────────
export type DayKey = { y: number; m: number; d: number };

type HourPoint = DayKey & { hh: number; label?: string };
type DayPoint = DayKey & { label?: string };

export type ResolvedRange =
  | { mode: 'hour'; label: string; start: HourPoint; end: HourPoint }
  | { mode: 'day'; label: string; start: DayPoint; end: DayPoint };

export type TimePickerValue = {
  presetId: string;
  range: ResolvedRange;
};

const dateToObj = (d: Date): DayKey => ({
  y: d.getFullYear(),
  m: d.getMonth(),
  d: d.getDate(),
});
const objToDate = (o: DayKey, hh = 0) => new Date(o.y, o.m, o.d, hh, 0, 0, 0);
const isoOf = (o: DayKey) => o.y * 10000 + o.m * 100 + o.d;
const fmtDay = (o: DayKey) => `${o.m + 1}/${o.d}`;
const fmtDayYear = (o: DayKey) => `${o.y}/${o.m + 1}/${o.d}`;
const fmtHour = (hh: number) => `${String(hh).padStart(2, '0')}:00`;
const addDays = (o: DayKey, n: number): DayKey =>
  dateToObj(new Date(o.y, o.m, o.d + n));
const eqDay = (a?: DayKey, b?: DayKey) =>
  !!a && !!b && a.y === b.y && a.m === b.m && a.d === b.d;
const cmpDay = (a: DayKey, b: DayKey) => isoOf(a) - isoOf(b);
const daysBetween = (a: DayKey, b: DayKey) =>
  Math.round((objToDate(b).getTime() - objToDate(a).getTime()) / 86400000) + 1;

// ─── Presets ─────────────────────────────────────────────────────────
type PresetDef = { id: string; labelKey: string };

const PRESETS: PresetDef[] = [
  { id: '24h', labelKey: `${ns}.timePicker.preset.24h` },
  { id: '3d', labelKey: `${ns}.timePicker.preset.3d` },
  { id: '7d', labelKey: `${ns}.timePicker.preset.7d` },
  { id: '14d', labelKey: `${ns}.timePicker.preset.14d` },
  { id: '30d', labelKey: `${ns}.timePicker.preset.30d` },
];

export function resolvePreset(id: string, now: Date): ResolvedRange | null {
  const t = dateToObj(now);
  const h = now.getHours();
  switch (id) {
    case '24h':
      return {
        mode: 'hour',
        label: '最近 24 小时',
        start: { ...addDays(t, -1), hh: h },
        end: { ...t, hh: h },
      };
    case '3d':
      return {
        mode: 'day',
        label: '最近 3 天',
        start: addDays(t, -2),
        end: t,
      };
    case '7d':
      return {
        mode: 'day',
        label: '最近 7 天',
        start: addDays(t, -6),
        end: t,
      };
    case '14d':
      return {
        mode: 'day',
        label: '最近 14 天',
        start: addDays(t, -13),
        end: t,
      };
    case '30d':
      return {
        mode: 'day',
        label: '最近 30 天',
        start: addDays(t, -29),
        end: t,
      };
    default:
      return null;
  }
}

const SIDE_LABEL_KEYS: Record<string, string> = {
  '24h': `${ns}.timePicker.side.24h`,
  '3d': `${ns}.timePicker.side.3d`,
  '7d': `${ns}.timePicker.side.7d`,
  '14d': `${ns}.timePicker.side.14d`,
  '30d': `${ns}.timePicker.side.30d`,
};

function fmtRange(r: ResolvedRange | null): string {
  if (!r) return '';
  if (r.mode === 'hour') {
    if (eqDay(r.start, r.end)) {
      const endLbl = r.end.label || fmtHour(r.end.hh);
      return `${fmtDay(r.start)} ${fmtHour(r.start.hh)} – ${endLbl}`;
    }
    return `${fmtDay(r.start)} ${fmtHour(r.start.hh)} – ${fmtDay(r.end)} ${fmtHour(r.end.hh)}`;
  }
  if (eqDay(r.start, r.end)) return fmtDay(r.start);
  return `${fmtDay(r.start)} – ${fmtDay(r.end)}`;
}

// External helper: feed an API. Always returns a half-open [from, to] in
// the local timezone where `to` is end-of-day for day ranges.
export function rangeToDates(value: TimePickerValue): { from: Date; to: Date } {
  const r = value.range;
  if (r.mode === 'hour') {
    return {
      from: objToDate(r.start, r.start.hh),
      to: objToDate(r.end, r.end.hh),
    };
  }
  return {
    from: objToDate(r.start, 0),
    to: new Date(r.end.y, r.end.m, r.end.d, 23, 59, 59, 999),
  };
}

export function rangeToQueryWindow(value: TimePickerValue): {
  endTimestamp: number;
  from: Date;
  startTimestamp: number;
  to: Date;
} {
  const { from, to } = rangeToDates(value);
  return {
    endTimestamp: Math.floor(to.getTime() / 1000),
    from,
    startTimestamp: Math.floor(from.getTime() / 1000),
    to,
  };
}

// ─── Icons (inline, no dep) ──────────────────────────────────────────
function CalIcon({ size = 13 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox='0 0 14 14'
      fill='none'
      stroke='currentColor'
      strokeWidth='1.4'
      strokeLinecap='round'
      strokeLinejoin='round'
      aria-hidden='true'
    >
      <rect x='1.5' y='2.5' width='11' height='10' rx='1.5' />
      <path d='M1.5 5.5h11M4.5 1v3M9.5 1v3' />
    </svg>
  );
}

function ChevronDown({ size = 10 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox='0 0 10 10'
      fill='none'
      stroke='currentColor'
      strokeWidth='1.6'
      strokeLinecap='round'
      strokeLinejoin='round'
      aria-hidden='true'
    >
      <path d='M2 4l3 3 3-3' />
    </svg>
  );
}

function CheckIcon({ size = 10 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox='0 0 12 12'
      fill='none'
      stroke='currentColor'
      strokeWidth='2'
      strokeLinecap='round'
      strokeLinejoin='round'
      aria-hidden='true'
    >
      <path d='M2 6.5l3 3 5-6' />
    </svg>
  );
}

function NavLeft({ size = 10 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox='0 0 10 10'
      fill='none'
      stroke='currentColor'
      strokeWidth='1.6'
      strokeLinecap='round'
      strokeLinejoin='round'
      aria-hidden='true'
    >
      <path d='M6.5 2L3.5 5l3 3' />
    </svg>
  );
}

function NavRight({ size = 10 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox='0 0 10 10'
      fill='none'
      stroke='currentColor'
      strokeWidth='1.6'
      strokeLinecap='round'
      strokeLinejoin='round'
      aria-hidden='true'
    >
      <path d='M3.5 2l3 3-3 3' />
    </svg>
  );
}

// ─── Calendar (Monday-first) ─────────────────────────────────────────
// i18n key suffixes; the locale supplies the visible string ("月" /
// "Mon" / etc). Order is Mon → Sun to match the calendar layout.
const DOW_KEYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const;

type Cell = { d: number; m: number; y: number; out: boolean; iso: number };

function buildMonthCells(year: number, month: number): Cell[] {
  const first = new Date(year, month, 1);
  const last = new Date(year, month + 1, 0).getDate();
  const prevLast = new Date(year, month, 0).getDate();
  const firstDow = (first.getDay() + 6) % 7;
  const cells: Cell[] = [];
  for (let i = 0; i < 42; i += 1) {
    const slot = i - firstDow;
    let cellD: number;
    let cellM: number;
    let cellY: number;
    let out = false;
    if (slot < 0) {
      cellD = prevLast + slot + 1;
      cellM = month === 0 ? 11 : month - 1;
      cellY = month === 0 ? year - 1 : year;
      out = true;
    } else if (slot >= last) {
      cellD = slot - last + 1;
      cellM = month === 11 ? 0 : month + 1;
      cellY = month === 11 ? year + 1 : year;
      out = true;
    } else {
      cellD = slot + 1;
      cellM = month;
      cellY = year;
    }
    cells.push({
      d: cellD,
      m: cellM,
      y: cellY,
      out,
      iso: cellY * 10000 + cellM * 100 + cellD,
    });
  }
  return cells;
}

type CalendarMonthProps = {
  month: number;
  onCellClick: (d: DayKey) => void;
  onCellHover: (d: DayKey | null) => void;
  range?: { start?: DayKey; end?: DayKey } | null;
  today: DayKey;
  year: number;
};

function CalendarMonth({
  month,
  onCellClick,
  onCellHover,
  range,
  today,
  year,
}: CalendarMonthProps) {
  const { t } = useTranslation();
  const cells = useMemo(() => buildMonthCells(year, month), [year, month]);
  const startIso = range?.start ? isoOf(range.start) : null;
  const endIso = range?.end ? isoOf(range.end) : null;
  const todayIso = isoOf(today);

  return (
    <div className='dashv3-tp-cal-month'>
      <div className='dashv3-tp-cal-dow'>
        {DOW_KEYS.map((key) => (
          <span key={key}>{t(`${ns}.timePicker.dow.${key}`)}</span>
        ))}
      </div>
      {Array.from({ length: 6 }).map((_, row) => (
        <div key={row} className='dashv3-tp-cal-row'>
          {cells.slice(row * 7, row * 7 + 7).map((c, i) => {
            const inRange =
              !c.out &&
              startIso !== null &&
              endIso !== null &&
              c.iso >= startIso &&
              c.iso <= endIso;
            const isStart = !c.out && c.iso === startIso;
            const isEnd = !c.out && c.iso === endIso;
            const isEdge =
              isStart && isEnd
                ? 'both'
                : isStart
                  ? 'left'
                  : isEnd
                    ? 'right'
                    : null;
            const isToday = !c.out && c.iso === todayIso;
            const isFuture = !c.out && c.iso > todayIso;
            const disabled = c.out || isFuture;
            return (
              <button
                key={i}
                type='button'
                className='dashv3-tp-cal-cell'
                data-out={c.out || undefined}
                data-future={isFuture || undefined}
                data-disabled={disabled || undefined}
                data-range={inRange || undefined}
                data-edge={isEdge || undefined}
                data-endpoint={isStart || isEnd || undefined}
                data-today={isToday || undefined}
                disabled={disabled}
                onClick={
                  disabled
                    ? undefined
                    : () => onCellClick({ y: c.y, m: c.m, d: c.d })
                }
                onMouseEnter={
                  disabled
                    ? undefined
                    : () => onCellHover({ y: c.y, m: c.m, d: c.d })
                }
                onMouseLeave={() => onCellHover(null)}
              >
                <span>{c.d}</span>
              </button>
            );
          })}
        </div>
      ))}
    </div>
  );
}

type CalendarDualProps = {
  left: { month: number; year: number };
  onCellClick: (d: DayKey) => void;
  onCellHover: (d: DayKey | null) => void;
  onNext: () => void;
  onPrev: () => void;
  range?: { start?: DayKey; end?: DayKey } | null;
  right: { month: number; year: number };
  today: DayKey;
};

function CalendarDual({
  left,
  onCellClick,
  onCellHover,
  onNext,
  onPrev,
  range,
  right,
  today,
}: CalendarDualProps) {
  const { t } = useTranslation();
  const formatTitle = (y: number, m: number) =>
    t(`${ns}.timePicker.calMonth`, { year: y, month: m + 1 });
  return (
    <div className='dashv3-tp-cal'>
      <div className='dashv3-tp-cal-head'>
        <button
          type='button'
          className='dashv3-tp-cal-nav'
          onClick={onPrev}
          aria-label={t(`${ns}.timePicker.prevMonth`)}
        >
          <NavLeft />
        </button>
        <div className='dashv3-tp-cal-head-titles'>
          <span>{formatTitle(left.year, left.month)}</span>
          <span>{formatTitle(right.year, right.month)}</span>
        </div>
        <button
          type='button'
          className='dashv3-tp-cal-nav'
          onClick={onNext}
          aria-label={t(`${ns}.timePicker.nextMonth`)}
        >
          <NavRight />
        </button>
      </div>
      <div className='dashv3-tp-cal-grid'>
        <CalendarMonth
          year={left.year}
          month={left.month}
          range={range}
          today={today}
          onCellClick={onCellClick}
          onCellHover={onCellHover}
        />
        <CalendarMonth
          year={right.year}
          month={right.month}
          range={range}
          today={today}
          onCellClick={onCellClick}
          onCellHover={onCellHover}
        />
      </div>
    </div>
  );
}

// ─── InteractiveTimePicker ───────────────────────────────────────────
type Draft = { end: DayKey | null; start: DayKey | null };

type CompactPopoverLayout = {
  left: number;
  maxHeight: number;
  top: number;
  width: number;
};

type TimePickerProps = {
  /* Compact renders the trigger as a bare chevron so the picker can ride as
     the last cell of the overview's 24h/7d/30d segmented control — the head
     shows one range control, but 3d / 14d / custom stay reachable. */
  compact?: boolean;
  now?: Date;
  onChange: (value: TimePickerValue) => void;
  value: TimePickerValue;
};

export function InteractiveTimePicker({
  compact = false,
  now: nowProp,
  onChange,
  value,
}: TimePickerProps) {
  const { t } = useTranslation();
  const now = nowProp ?? new Date();
  const today = dateToObj(now);

  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<Draft>({ start: null, end: null });
  const [hoverDay, setHoverDay] = useState<DayKey | null>(null);
  const [focused, setFocused] = useState<string | null>(null);
  const [compactPopoverLayout, setCompactPopoverLayout] =
    useState<CompactPopoverLayout | null>(null);
  const [monthAnchor, setMonthAnchor] = useState<{ m: number; y: number }>(
    () => ({ y: today.y, m: today.m }),
  );
  const rootRef = useRef<HTMLDivElement>(null);
  const popoverRef = useRef<HTMLDivElement>(null);

  const reset = useCallback(() => {
    setDraft({ start: null, end: null });
    setHoverDay(null);
    setFocused(null);
  }, []);

  const close = useCallback(() => {
    setOpen(false);
    reset();
  }, [reset]);

  // Outside click closes
  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        close();
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [open, close]);

  // The compact trigger can sit anywhere in a wrapped header. Preserve the
  // 540px desktop panel, shrink it only when the viewport requires it, and
  // collision-place it above or below the trigger. A bounded scroll container
  // keeps the calendar footer reachable on short landscape screens.
  useLayoutEffect(() => {
    if (!compact || !open) return;
    const updatePosition = () => {
      const trigger = rootRef.current?.getBoundingClientRect();
      const popover = popoverRef.current;
      if (!trigger || !popover) return;

      const viewportPadding = 16;
      const width = Math.min(
        540,
        Math.max(0, window.innerWidth - viewportPadding * 2),
      );
      const maxHeight = Math.max(0, window.innerHeight - viewportPadding * 2);
      const height = Math.min(popover.scrollHeight, maxHeight);
      const belowTop = trigger.bottom + 6;
      const aboveTop = trigger.top - height - 6;
      const top =
        belowTop + height <= window.innerHeight - viewportPadding
          ? belowTop
          : aboveTop >= viewportPadding
            ? aboveTop
            : viewportPadding;
      const maxLeft = Math.max(
        viewportPadding,
        window.innerWidth - width - viewportPadding,
      );
      const left = Math.min(
        Math.max(viewportPadding, trigger.right - width),
        maxLeft,
      );
      const next = { left, maxHeight, top, width };
      setCompactPopoverLayout((current) =>
        current &&
        current.left === next.left &&
        current.maxHeight === next.maxHeight &&
        current.top === next.top &&
        current.width === next.width
          ? current
          : next,
      );
    };
    updatePosition();
    const resizeObserver =
      typeof ResizeObserver === 'undefined'
        ? null
        : new ResizeObserver(updatePosition);
    if (popoverRef.current) resizeObserver?.observe(popoverRef.current);
    window.addEventListener('resize', updatePosition);
    document.addEventListener('scroll', updatePosition, true);
    return () => {
      resizeObserver?.disconnect();
      window.removeEventListener('resize', updatePosition);
      document.removeEventListener('scroll', updatePosition, true);
    };
  }, [compact, open]);

  // Keyboard nav
  useEffect(() => {
    if (!open) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        close();
        return;
      }
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        const cur = focused || value.presetId;
        const idx = PRESETS.findIndex((p) => p.id === cur);
        const delta = e.key === 'ArrowDown' ? 1 : -1;
        const next =
          PRESETS[(idx + delta + PRESETS.length) % PRESETS.length].id;
        setFocused(next);
        return;
      }
      if (e.key === 'Enter' && focused) {
        e.preventDefault();
        handlePresetClick(focused);
      }
    };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, focused, value.presetId]);

  const handlePresetClick = useCallback(
    (id: string) => {
      const range = resolvePreset(id, now);
      if (range) {
        onChange({ presetId: id, range });
      }
      close();
    },
    [onChange, now, close],
  );

  // Custom calendar: single click semantics with today cap.
  //   1st click → set start (must be ≤ today, enforced by disabled cells)
  //   2nd click → set end (auto-swap if before start)
  //   3rd click → reset, start over
  // No double-click anywhere.
  const handleCellClick = useCallback(
    (day: DayKey) => {
      // Defense in depth — disabled cells already block onClick, but
      // never let a future day slip through.
      if (cmpDay(day, today) > 0) return;
      setDraft((d) => {
        if (!d.start || (d.start && d.end)) {
          return { start: day, end: null };
        }
        const start = cmpDay(d.start, day) <= 0 ? d.start : day;
        const end = cmpDay(d.start, day) <= 0 ? day : d.start;
        return { start, end };
      });
      setHoverDay(null);
    },
    [today],
  );

  const handleApply = useCallback(() => {
    if (draft.start && draft.end) {
      onChange({
        presetId: 'custom',
        range: {
          mode: 'day',
          label: '自定义',
          start: draft.start,
          end: draft.end,
        },
      });
    }
    close();
  }, [draft, onChange, close]);

  const inDraft = !!draft.start;

  // Range to highlight in the calendar
  const calRange = useMemo<{ end?: DayKey; start?: DayKey } | null>(() => {
    if (inDraft && draft.start) {
      if (draft.end) {
        return { start: draft.start, end: draft.end };
      }
      if (hoverDay) {
        // Clamp hover to today so the preview band doesn't suggest
        // selecting a future date.
        const clamped = cmpDay(hoverDay, today) > 0 ? today : hoverDay;
        const start = cmpDay(draft.start, clamped) <= 0 ? draft.start : clamped;
        const end = cmpDay(draft.start, clamped) <= 0 ? clamped : draft.start;
        return { start, end };
      }
      return { start: draft.start, end: draft.start };
    }
    if (value.range.mode === 'day') {
      return { start: value.range.start, end: value.range.end };
    }
    return null;
  }, [inDraft, draft, hoverDay, value, today]);

  const rightMonth = { year: monthAnchor.y, month: monthAnchor.m };
  const leftMonth =
    monthAnchor.m === 0
      ? { year: monthAnchor.y - 1, month: 11 }
      : { year: monthAnchor.y, month: monthAnchor.m - 1 };

  const navPrev = () =>
    setMonthAnchor((m) =>
      m.m === 0 ? { y: m.y - 1, m: 11 } : { y: m.y, m: m.m - 1 },
    );
  const navNext = () =>
    setMonthAnchor((m) => {
      const next = m.m === 11 ? { y: m.y + 1, m: 0 } : { y: m.y, m: m.m + 1 };
      // Don't navigate past current month — left panel would then show
      // months entirely in the future.
      if (next.y > today.y || (next.y === today.y && next.m > today.m)) {
        return m;
      }
      return next;
    });

  const showFooter = inDraft;
  const isCustom = value.presetId === 'custom';
  const presetSideKey = SIDE_LABEL_KEYS[value.presetId];
  const sideText = isCustom
    ? value.range && value.range.mode === 'day'
      ? t(`${ns}.timePicker.side.customDays`, {
          count: daysBetween(value.range.start, value.range.end),
        })
      : null
    : presetSideKey
      ? t(presetSideKey)
      : null;
  const mainText =
    isCustom && value.range && value.range.mode === 'day'
      ? `${fmtDay(value.range.start)} – ${fmtDay(value.range.end)}`
      : fmtRange(value.range);

  const triggerOpen = () => {
    setOpen((o) => !o);
    setFocused(value.presetId);
  };

  return (
    <div ref={rootRef} className='dashv3-tp-root'>
      {compact ? (
        <button
          type='button'
          className='dashv3-tp-btn dashv3-tp-btn--compact'
          data-open={open || undefined}
          data-accent={isCustom || undefined}
          onClick={triggerOpen}
          aria-haspopup='dialog'
          aria-expanded={open}
          aria-label={t(`${ns}.timePicker.dialogLabel`)}
          title={sideText ? `${mainText} · ${sideText}` : mainText}
        >
          <CalIcon />
        </button>
      ) : (
        <button
          type='button'
          className='dashv3-tp-btn'
          data-open={open || undefined}
          data-accent={isCustom || undefined}
          onClick={triggerOpen}
          aria-haspopup='dialog'
          aria-expanded={open}
        >
          <CalIcon />
          <span className='dashv3-tp-range'>{mainText}</span>
          {sideText ? (
            <span className='dashv3-tp-meta'>· {sideText}</span>
          ) : null}
          <ChevronDown />
        </button>
      )}

      {open ? (
        <div
          ref={popoverRef}
          className='dashv3-tp-popover'
          role='dialog'
          aria-label={t(`${ns}.timePicker.dialogLabel`)}
          style={
            compact && compactPopoverLayout !== null
              ? {
                  left: compactPopoverLayout.left,
                  marginTop: 0,
                  maxHeight: compactPopoverLayout.maxHeight,
                  overflowY: 'auto',
                  position: 'fixed',
                  top: compactPopoverLayout.top,
                  width: compactPopoverLayout.width,
                }
              : undefined
          }
        >
          <CalendarDual
            left={leftMonth}
            right={rightMonth}
            range={calRange}
            today={today}
            onPrev={navPrev}
            onNext={navNext}
            onCellClick={handleCellClick}
            onCellHover={setHoverDay}
          />
          <div
            className='dashv3-tp-quick'
            role='radiogroup'
            aria-label={t(`${ns}.timePicker.quickLabel`)}
          >
            <span className='dashv3-tp-quick-label'>
              {t(`${ns}.timePicker.quickLabel`)}
            </span>
            {PRESETS.map((p) => {
              const isSel = !inDraft && p.id === value.presetId;
              const isFoc = !isSel && p.id === focused;
              return (
                <button
                  key={p.id}
                  type='button'
                  className='dashv3-tp-quick-pill'
                  data-selected={isSel || undefined}
                  data-focus={isFoc || undefined}
                  role='radio'
                  aria-checked={isSel}
                  onMouseEnter={() => setFocused(p.id)}
                  onClick={() => handlePresetClick(p.id)}
                >
                  {t(p.labelKey)}
                  {isSel ? (
                    <span className='dashv3-tp-quick-pill-tick'>
                      <CheckIcon />
                    </span>
                  ) : null}
                </button>
              );
            })}
          </div>
          {showFooter ? (
            <div className='dashv3-tp-foot'>
              <span className='dashv3-tp-foot-info'>
                {draft.start && draft.end
                  ? t(`${ns}.timePicker.draftComplete`, {
                      start: fmtDayYear(draft.start),
                      end: fmtDayYear(draft.end),
                      count: daysBetween(draft.start, draft.end),
                    })
                  : t(`${ns}.timePicker.draftAwaitingEnd`, {
                      start: fmtDayYear(draft.start!),
                    })}
              </span>
              <div style={{ display: 'flex', gap: 8 }}>
                <button
                  type='button'
                  className='dashv3-btn dashv3-btn--sm'
                  onClick={close}
                >
                  {t(`${ns}.timePicker.cancel`)}
                </button>
                <button
                  type='button'
                  className='dashv3-btn dashv3-btn--primary dashv3-btn--sm'
                  disabled={!(draft.start && draft.end)}
                  onClick={handleApply}
                >
                  {t(`${ns}.timePicker.apply`)}
                </button>
              </div>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

// ─── Defaults ────────────────────────────────────────────────────────
export const DEFAULT_PRESET_ID = '7d';

export function createDefaultValue(now: Date = new Date()): TimePickerValue {
  const range = resolvePreset(DEFAULT_PRESET_ID, now);
  if (!range) {
    throw new Error('default preset failed to resolve');
  }
  return { presetId: DEFAULT_PRESET_ID, range };
}

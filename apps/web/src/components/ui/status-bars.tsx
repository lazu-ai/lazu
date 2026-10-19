import {
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
  type MouseEvent,
} from 'react';
import { createPortal } from 'react-dom';
import { cn } from '../../lib/cn';
import './status-bars.css';

/* The health page's line of thin bars, as a component: one bar per time
   slice, coloured by what the slice saw, with the same ink tooltip on hover
   and focus. Every surface that shows a model's record over time draws it
   with this, so a bar reads the same on the plaza, in the detail modal and in
   model operations. */

/** `future` is a slice the record has not reached yet — a watch that runs
 *  for a week draws its whole week from the first day. */
export type StatusBarTone =
  | 'ok'
  | 'warn'
  | 'bad'
  | 'none'
  | 'blocked'
  | 'future';

/** Status pages print availability to the precision that separates good
 *  weeks: two decimals above 99%, one below it. */
export const formatUptime = (value?: number | null) => {
  if (value == null) return '—';
  const percent = value * 100;
  if (value >= 1) return '100.0%';
  return `${Math.min(percent, 99.99).toFixed(value >= 0.99 ? 2 : 1)}%`;
};

/** One slice's availability: whole numbers where they are whole (0%, 100%),
 *  one decimal otherwise. */
export const formatSliceRate = (value?: number | null) => {
  if (value == null) return '—';
  const percent = Number(
    (value < 1 ? Math.min(value * 100, 99.9) : 100).toFixed(1),
  );
  return `${Number.isInteger(percent) ? percent.toFixed(0) : percent.toFixed(1)}%`;
};

/** "9月28日 14:00 – 16:00" in the reader's locale, as the health page prints
 *  a slice. */
export const formatSliceWhen = (
  startAt: string | undefined,
  seconds: number,
  locale?: string,
) => {
  if (!startAt) return '';
  const start = new Date(startAt);
  if (Number.isNaN(start.getTime())) return '';
  const end = new Date(start.getTime() + seconds * 1000);
  const { day, time } = sliceFormatters(locale);
  return `${day.format(start)} ${time.format(start)} – ${time.format(end)}`;
};

/* Building an Intl.DateTimeFormat is expensive and every bar of every
   record calls the formatter, so one pair per locale is kept and reused. */
const sliceFormatterCache = new Map<
  string,
  { day: Intl.DateTimeFormat; time: Intl.DateTimeFormat }
>();
const sliceFormatters = (locale?: string) => {
  const key = locale ?? '';
  let pair = sliceFormatterCache.get(key);
  if (!pair) {
    pair = {
      day: new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric' }),
      time: new Intl.DateTimeFormat(locale, {
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      }),
    };
    sliceFormatterCache.set(key, pair);
  }
  return pair;
};

export type StatusBarSlice = {
  tone: StatusBarTone;
  /** The slice's time range, e.g. "9月28日 14:00 – 16:00". */
  when: string;
  /** The verdict line: "正常 · 可用 100%". */
  verdict: string;
  /** Optional third line (operators see sample counts here). */
  note?: string;
};

type StatusBarsProps = {
  slices: StatusBarSlice[];
  /** Names the group for assistive tech, e.g. "稳定线路过去 7 天的记录". */
  label: string;
  /** Bar height in px; 12 matches the health page. */
  height?: number;
  className?: string;
};

export function StatusBars({
  slices,
  label,
  height = 12,
  className,
}: StatusBarsProps) {
  const [hoverIndex, setHoverIndex] = useState(-1);
  const rowRef = useRef<HTMLDivElement>(null);
  const [focusIndex, setFocusIndex] = useState(slices.length - 1);
  const count = slices.length;
  const hovered = hoverIndex >= 0 ? slices[hoverIndex] : undefined;

  const onMove = (event: MouseEvent<HTMLElement>) => {
    const index = Number((event.target as HTMLElement).dataset.index);
    if (Number.isInteger(index)) setHoverIndex(index);
  };
  const onKeyDown = (
    event: KeyboardEvent<HTMLButtonElement>,
    index: number,
  ) => {
    if (event.key === 'Escape') {
      setHoverIndex(-1);
      return;
    }
    const next = {
      ArrowLeft: Math.max(0, index - 1),
      ArrowRight: Math.min(count - 1, index + 1),
      Home: 0,
      End: count - 1,
    }[event.key];
    if (next == null) return;
    event.preventDefault();
    const target = event.currentTarget.parentElement?.children[next];
    if (target instanceof HTMLButtonElement) target.focus();
  };

  // Anchored to its bar and slid along itself by the same share, so a bar at
  // either end keeps its tooltip over the row. The tip is drawn in a portal
  // with fixed coordinates: bars live inside cards, table cells and modals
  // that clip their overflow.
  const shareValue = ((hoverIndex + 0.5) / Math.max(count, 1)) * 100;
  const rect = hovered ? rowRef.current?.getBoundingClientRect() : undefined;
  const tipStyle: CSSProperties | undefined = rect
    ? {
        left: rect.left + (rect.width * shareValue) / 100,
        top: rect.top,
        transform: `translate(-${shareValue}%, calc(-100% - 10px))`,
      }
    : undefined;

  return (
    <div
      className={cn('lazu-status-bars', className)}
      onMouseLeave={() => setHoverIndex(-1)}
      onMouseMove={onMove}
    >
      <div
        aria-label={label}
        className='lazu-status-bars__row'
        ref={rowRef}
        role='group'
        style={{ height }}
      >
        {slices.map((slice, index) => (
          <button
            aria-label={[slice.when, slice.verdict, slice.note]
              .filter(Boolean)
              .join(' · ')}
            className={cn(
              'lazu-status-bar',
              `lazu-status-bar--${slice.tone}`,
              hoverIndex === index && 'is-hovered',
            )}
            data-index={index}
            data-owns-chrome
            key={index}
            onBlur={() => setHoverIndex(-1)}
            onClick={(event) => {
              // Bars often sit inside a clickable card or row; inspecting a
              // slice must not also open what the card opens.
              event.stopPropagation();
              setHoverIndex(index);
            }}
            onFocus={() => {
              setFocusIndex(index);
              setHoverIndex(index);
            }}
            onKeyDown={(event) => onKeyDown(event, index)}
            tabIndex={index === Math.min(focusIndex, count - 1) ? 0 : -1}
            type='button'
          />
        ))}
      </div>
      {hovered && tipStyle && typeof document !== 'undefined'
        ? createPortal(
            <div className='lazu-status-bars__tip' style={tipStyle}>
              {hovered.when ? (
                <span className='lazu-status-bars__when'>{hovered.when}</span>
              ) : null}
              <span
                className={`lazu-status-bars__verdict lazu-status-bars__verdict--${hovered.tone}`}
              >
                {hovered.verdict}
              </span>
              {hovered.note ? (
                <span className='lazu-status-bars__note'>{hovered.note}</span>
              ) : null}
            </div>,
            document.body,
          )
        : null}
    </div>
  );
}

/** The colour legend that sits above a set of status bars. */
export function StatusBarsLegend({
  items,
  caption,
  className,
}: {
  items: { tone: StatusBarTone; label: string }[];
  caption?: string;
  className?: string;
}) {
  return (
    <span className={cn('lazu-status-legend', className)}>
      {caption ? (
        <span className='lazu-status-legend__caption'>{caption}</span>
      ) : null}
      {items.map((item) => (
        <span className='lazu-status-legend__item' key={item.tone}>
          <i
            aria-hidden='true'
            className={`lazu-status-bar lazu-status-bar--${item.tone}`}
          />
          {item.label}
        </span>
      ))}
    </span>
  );
}

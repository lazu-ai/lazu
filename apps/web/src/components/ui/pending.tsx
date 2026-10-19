/* What a figure looks like before its answer arrives.
 *
 * The bug this exists to stop: a page reads an unanswered query as an answer —
 * `data ?? 0`, an empty array as "nothing here" — and states it. The console
 * overview told a paying account "本区间没有请求" and then corrected itself one
 * round trip later; the usage page put "共 0 条" under a table that was still
 * loading. A number nobody knows yet is not zero, and the honest shape for it
 * is the space it will occupy.
 *
 * Sized in `ch` so the bar sits on the same line box as the text it stands in
 * for: swapping it for the real figure changes no height and moves nothing.
 */
import { cn } from '../../lib/cn';
import type { CSSProperties, ReactNode } from 'react';

type PendingValueProps = {
  /** Roughly how many characters the real value takes, e.g. `$12.34` → 6. */
  chars?: number;
  className?: string;
  /** Screen-reader wording; the bar itself is decoration. */
  label?: string;
};

export function PendingValue({
  chars = 4,
  className,
  label,
}: PendingValueProps) {
  return (
    <span
      aria-label={label}
      className={cn('lazu-pending', className)}
      role={label ? 'status' : undefined}
      style={{ '--lazu-pending-chars': `${chars}ch` } as CSSProperties}
    >
      {label ? <span className='sr-only'>{label}</span> : null}
    </span>
  );
}

type PendingProps = {
  children: ReactNode;
  /** Roughly how many characters the real value takes. */
  chars?: number;
  className?: string;
  /** True while the answer is outstanding — NOT "the answer was empty". */
  pending: boolean;
};

/** The same, as a wrapper: `<Pending pending={q.isPending} chars={6}>{value}</Pending>` */
export function Pending({ children, chars, className, pending }: PendingProps) {
  if (!pending) return <>{children}</>;
  return <PendingValue chars={chars} className={className} />;
}

type PendingBlockProps = {
  className?: string;
  /** The height the answer will occupy, so the page below it never moves. */
  height: number | string;
  label?: string;
};

/** A reserved rectangle for something taller than a line: a chart, a document,
 *  a panel. Holds the space whether or not anything is drawn in it yet. */
export function PendingBlock({ className, height, label }: PendingBlockProps) {
  return (
    <div
      aria-label={label}
      className={cn('lazu-pending-block', className)}
      role={label ? 'status' : undefined}
      style={{ height }}
    />
  );
}

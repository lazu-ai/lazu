import { cn } from '../../lib/cn';
import type { ReactNode } from 'react';

type QuotaUsageMeterProps = {
  ariaLabel?: string;
  className?: string;
  description?: ReactNode;
  label: ReactNode;
  percent?: number | null;
  tone?: 'accent' | 'danger' | 'success';
  tooltip?: string;
};

/* Amounts draw in ink like the figures beside them (BRAND.md G1 rule 4);
   teal is for charts and selection. */
const toneClasses = {
  accent: 'bg-[var(--lazu-ink)]',
  danger: 'bg-[var(--ui-danger)]',
  success: 'bg-[var(--ui-success)]',
};

export function QuotaUsageMeter({
  ariaLabel,
  className,
  description,
  label,
  percent,
  tone = 'accent',
  tooltip,
}: QuotaUsageMeterProps) {
  const hasPercent = typeof percent === 'number' && Number.isFinite(percent);
  const width = hasPercent ? Math.min(100, Math.max(0, percent)) : 0;

  return (
    <div
      aria-label={ariaLabel}
      className={cn('min-w-[7.5rem] max-w-[9rem]', className)}
      title={tooltip}
    >
      <div className='whitespace-nowrap font-medium text-[var(--ui-text)]'>
        {label}
      </div>
      {hasPercent ? (
        <div className='mt-2 h-1 w-full overflow-hidden rounded-full bg-[var(--ui-bg-muted)]'>
          <div
            className={cn(
              'h-full rounded-full transition-[width]',
              toneClasses[tone],
            )}
            style={{
              minWidth: width > 0 ? '3px' : undefined,
              width: `${width}%`,
            }}
          />
        </div>
      ) : null}
      {description ? (
        <div className='mt-1 truncate text-xs text-[var(--ui-text-secondary)]'>
          {description}
        </div>
      ) : null}
    </div>
  );
}

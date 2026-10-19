import React from 'react';
import { cn } from '../../lib/cn';

type StatusDotTone = 'success' | 'warning' | 'danger' | 'info' | 'neutral';

type StatusDotProps = React.HTMLAttributes<HTMLSpanElement> & {
  tone?: StatusDotTone;
  pulse?: boolean;
};

export function StatusDot({
  children,
  className,
  tone = 'neutral',
  pulse = false,
  ...props
}: StatusDotProps) {
  return (
    <span
      className={cn(
        'lazu-status-dot',
        `lazu-status-dot--${tone}`,
        pulse && 'lazu-status-dot--pulse',
        className,
      )}
      {...props}
    >
      <span className='lazu-status-dot__dot' aria-hidden='true' />
      {children ? <span>{children}</span> : null}
    </span>
  );
}

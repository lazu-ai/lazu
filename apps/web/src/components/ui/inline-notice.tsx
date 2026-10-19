import { AlertTriangle, CheckCircle2, CircleAlert, Info } from 'lucide-react';
import React from 'react';
import { cn } from '../../lib/cn';

export type InlineNoticeTone = 'danger' | 'info' | 'success' | 'warning';

export type InlineNoticeProps = Omit<
  React.HTMLAttributes<HTMLDivElement>,
  'title'
> & {
  actions?: React.ReactNode;
  description?: React.ReactNode;
  icon?: React.ReactNode;
  title: React.ReactNode;
  tone?: InlineNoticeTone;
};

const NOTICE_ICON: Record<InlineNoticeTone, typeof Info> = {
  danger: CircleAlert,
  info: Info,
  success: CheckCircle2,
  warning: AlertTriangle,
};

/**
 * The one canonical in-page notice — brand/foundations/feedback-system.md.
 * Shape carries the semantic alongside color so the state survives both a
 * grayscale print and a color-blind reader.
 */
export function InlineNotice({
  actions,
  className,
  description,
  icon,
  title,
  tone = 'info',
  ...props
}: InlineNoticeProps) {
  const ToneIcon = NOTICE_ICON[tone];

  return (
    <div
      className={cn(
        'lazu-inline-notice',
        `lazu-inline-notice--${tone}`,
        className,
      )}
      role={tone === 'danger' || tone === 'warning' ? 'alert' : 'status'}
      {...props}
    >
      <span className='lazu-inline-notice__icon'>
        {icon ?? <ToneIcon aria-hidden='true' size={16} />}
      </span>
      <span className='lazu-inline-notice__copy'>
        <strong>{title}</strong>
        {description ? <span>{description}</span> : null}
      </span>
      {actions ? (
        <span className='lazu-inline-notice__actions'>{actions}</span>
      ) : null}
    </div>
  );
}

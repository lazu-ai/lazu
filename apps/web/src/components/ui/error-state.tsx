/* A failure inside a surface: the link is broken, nothing else is.
 *
 * One hairline rule with a break in the middle and a 5px danger dot at the
 * break says "this did not come through" in the width of a line — and it is
 * the only semantic colour in the card states, which is what the brand rule
 * asks for: state only, small area only.
 *
 * The full-screen crash state is a different object; see paper-page.tsx.
 */
import React from 'react';
import { useTranslation } from 'react-i18next';
import { errorMessage, errorReference } from '../../lib/api-error';
import { cn } from '../../lib/cn';

type ErrorStateProps = Omit<React.HTMLAttributes<HTMLDivElement>, 'title'> & {
  action?: React.ReactNode;
  description?: React.ReactNode;
  /* The failure itself. Supplies the description (copy for its code) and
   * the fine line (LZ number and request ID) unless those are given. */
  error?: unknown;
  /* Mono footer line — a trace id the reader can quote when reporting. */
  fine?: React.ReactNode;
  title?: React.ReactNode;
};

export function ErrorState({
  action,
  className,
  description,
  error,
  fine,
  title,
  ...props
}: ErrorStateProps) {
  const { t } = useTranslation('errors');
  const shownTitle = title ?? t('local.loadFailed');
  const shownDescription =
    description ?? (error !== undefined ? errorMessage(error) : null);
  const shownFine =
    fine ?? (error !== undefined ? errorReference(error) || null : null);
  return (
    <div className={cn('lazu-error', className)} role='alert' {...props}>
      <span aria-hidden='true' className='lazu-error__break'>
        <span className='lazu-error__dot' />
      </span>
      <div className='lazu-state-title'>{shownTitle}</div>
      {shownDescription ? (
        <div className='lazu-state-description'>{shownDescription}</div>
      ) : null}
      {action ? <div className='lazu-state-actions'>{action}</div> : null}
      {shownFine ? <div className='lazu-state-fine'>{shownFine}</div> : null}
    </div>
  );
}

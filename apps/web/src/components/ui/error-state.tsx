/* A failure inside a surface: this part did not come through, nothing else
 * is affected.
 *
 * A white card drawn with one ink line, a danger icon and a danger title;
 * that is the only semantic colour in the card states. The retry sits on the
 * right, where the eye ends the sentence.
 *
 * The full-screen crash state is a different object; see paper-page.tsx.
 */
import { CircleAlert } from 'lucide-react';
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
      <div className='lazu-error__card'>
        <CircleAlert
          aria-hidden='true'
          className='lazu-error__icon'
          size={16}
        />
        <div className='lazu-error__text'>
          <div className='lazu-state-title'>{shownTitle}</div>
          {shownDescription ? (
            <div className='lazu-state-description'>{shownDescription}</div>
          ) : null}
          {shownFine ? (
            <div className='lazu-state-fine'>{shownFine}</div>
          ) : null}
        </div>
        {action ? <div className='lazu-error__action'>{action}</div> : null}
      </div>
    </div>
  );
}

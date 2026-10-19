/* Waiting, said as quietly as possible.
 *
 * A view that is still loading is by definition not ready to be looked at,
 * so the wait carries no figure: one 15px hairline ring and one line of text.
 * The 44px outlined icon box this replaced was the loudest object on screen
 * at the exact moment there was nothing to see.
 *
 * `--inline` is the same object laid out in a row for "loading more rows", so
 * a single view never shows two different kinds of wait.
 */
import React from 'react';
import { cn } from '../../lib/cn';

type LoadingStateProps = Omit<React.HTMLAttributes<HTMLDivElement>, 'title'> & {
  description?: React.ReactNode;
  title?: React.ReactNode;
};

export function LoadingState({
  className,
  description,
  title = 'Loading',
  ...props
}: LoadingStateProps) {
  return (
    <div
      aria-live='polite'
      className={cn('lazu-loading', className)}
      role='status'
      {...props}
    >
      <span className='lazu-loading__row'>
        <span aria-hidden='true' className='lazu-loading__spinner' />
        <span className='lazu-state-title'>{title}</span>
      </span>
      {description ? (
        <div className='lazu-state-description'>{description}</div>
      ) : null}
    </div>
  );
}

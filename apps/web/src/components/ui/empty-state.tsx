/* Nothing here yet — and, when possible, somewhere to put the first thing.
 *
 * The dashed slot is the whole design: in an interface a dashed outline
 * already reads as "something goes here", so the empty state becomes a target
 * rather than an apology, and the first action lives inside it. Read-only or
 * filtered empty results have no such action, so they stay as quiet text
 * instead of promising an add/drop target that does not exist.
 *
 * The icon-in-a-box that used to sit here is gone. A new Workspace shows this
 * card on nearly every page at once, and an illustration repeated eight times
 * down a session stops being a picture and becomes furniture.
 */
import React from 'react';
import { cn } from '../../lib/cn';
import type { LucideIcon } from 'lucide-react';

type EmptyStateIcon = LucideIcon | React.ReactElement;

type EmptyStateProps = Omit<React.HTMLAttributes<HTMLDivElement>, 'title'> & {
  action?: React.ReactNode;
  description?: React.ReactNode;
  /* Accepted for source compatibility with existing call sites; the empty
     state carries no figure, so it is not rendered. */
  icon?: EmptyStateIcon;
  title?: React.ReactNode;
};

export function EmptyState({
  action,
  className,
  description,
  icon: _icon,
  title = 'No data',
  ...props
}: EmptyStateProps) {
  const content = (
    <>
      <div className='lazu-state-title'>{title}</div>
      {description ? (
        <div className='lazu-state-description'>{description}</div>
      ) : null}
      {action ? <div className='lazu-state-actions'>{action}</div> : null}
    </>
  );

  return (
    <div className={cn('lazu-empty', className)} {...props}>
      {action ? (
        <div className='lazu-empty__slot'>{content}</div>
      ) : (
        <div className='lazu-empty__plain'>{content}</div>
      )}
    </div>
  );
}

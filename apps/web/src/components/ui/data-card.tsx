import React from 'react';
import { cn } from '../../lib/cn';

type DataCardProps = Omit<React.HTMLAttributes<HTMLElement>, 'title'> & {
  actions?: React.ReactNode;
  description?: React.ReactNode;
  footer?: React.ReactNode;
  title?: React.ReactNode;
};

export function DataCard({
  actions,
  children,
  className,
  description,
  footer,
  title,
  ...props
}: DataCardProps) {
  return (
    <section className={cn('lazu-data-card', className)} {...props}>
      {title || description || actions ? (
        <div className='lazu-data-card__header'>
          <div>
            {title ? (
              <div className='lazu-data-card__title'>{title}</div>
            ) : null}
            {description ? (
              <div className='lazu-data-card__description'>{description}</div>
            ) : null}
          </div>
          {actions}
        </div>
      ) : null}
      <div className='lazu-data-card__body'>{children}</div>
      {footer ? <div className='lazu-data-card__footer'>{footer}</div> : null}
    </section>
  );
}

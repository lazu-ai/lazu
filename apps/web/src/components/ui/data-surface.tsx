import React from 'react';
import { cn } from '../../lib/cn';

type DataSurfaceProps = Omit<React.HTMLAttributes<HTMLElement>, 'title'> & {
  actions?: React.ReactNode;
  description?: React.ReactNode;
  title?: React.ReactNode;
};

export function DataSurface({
  actions,
  children,
  className,
  description,
  title,
  ...props
}: DataSurfaceProps) {
  return (
    <section className={cn('lazu-data-surface', className)} {...props}>
      {title || description || actions ? (
        <div className='lazu-data-surface__header'>
          <div>
            {title ? (
              <div className='lazu-data-surface__title'>{title}</div>
            ) : null}
            {description ? (
              <div className='lazu-data-surface__description'>
                {description}
              </div>
            ) : null}
          </div>
          {actions}
        </div>
      ) : null}
      <div className='lazu-data-surface__body'>{children}</div>
    </section>
  );
}

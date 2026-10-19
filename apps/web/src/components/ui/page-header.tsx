import React from 'react';
import { cn } from '../../lib/cn';

type PageHeaderProps = {
  actions?: React.ReactNode;
  children?: React.ReactNode;
  className?: string;
  description?: React.ReactNode;
  eyebrow?: React.ReactNode;
  title?: React.ReactNode;
};

export function PageHeader({
  actions,
  children,
  className,
  description,
  eyebrow,
  title,
}: PageHeaderProps) {
  return (
    <header className={cn('lazu-page-header', className)}>
      <div className='lazu-page-header__content'>
        {eyebrow ? (
          <div className='lazu-page-header__eyebrow'>{eyebrow}</div>
        ) : null}
        {title ? <h1 className='lazu-page-header__title'>{title}</h1> : null}
        {description ? (
          <div className='lazu-page-header__description'>{description}</div>
        ) : null}
        {children}
      </div>
      {actions ? (
        <div className='lazu-page-header__actions'>{actions}</div>
      ) : null}
    </header>
  );
}

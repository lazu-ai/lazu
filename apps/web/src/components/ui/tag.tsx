import { X } from 'lucide-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '../../lib/cn';

type TagProps = React.HTMLAttributes<HTMLSpanElement> & {
  onRemove?: React.MouseEventHandler<HTMLButtonElement>;
  removable?: boolean;
};

export function Tag({
  children,
  className,
  onRemove,
  removable = false,
  ...props
}: TagProps) {
  const { t } = useTranslation();

  return (
    <span className={cn('lazu-tag', className)} {...props}>
      {children}
      {removable || onRemove ? (
        <button
          type='button'
          className='lazu-tag-remove'
          aria-label={t('legacy.i18n.delete.3755f56f2f')}
          onClick={onRemove}
        >
          <X size={12} />
        </button>
      ) : null}
    </span>
  );
}

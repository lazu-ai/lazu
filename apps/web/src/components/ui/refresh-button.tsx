import { RefreshCw } from 'lucide-react';
import { Button } from './button';
import { cn } from '../../lib/cn';
import type { ButtonProps } from './button';

type RefreshButtonProps = Omit<ButtonProps, 'loading' | 'status'> & {
  refreshing?: boolean;
};

export function RefreshButton({
  children = 'Refresh',
  className,
  refreshing = false,
  ...props
}: RefreshButtonProps) {
  return (
    <Button
      className={cn('lazu-refresh-button', className)}
      data-refreshing={refreshing || undefined}
      disabled={props.disabled || refreshing}
      aria-busy={refreshing || undefined}
      {...props}
    >
      <RefreshCw
        aria-hidden='true'
        className={cn(refreshing && 'lazu-spin-icon')}
        size={16}
      />
      {children}
    </Button>
  );
}

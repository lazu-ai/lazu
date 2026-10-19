import React from 'react';
import { cn } from '../../lib/cn';

type ToolbarProps = React.HTMLAttributes<HTMLDivElement>;

export function Toolbar({ children, className, ...props }: ToolbarProps) {
  return (
    <div className={cn('lazu-toolbar', className)} {...props}>
      {children}
    </div>
  );
}

export function ToolbarGroup({ children, className, ...props }: ToolbarProps) {
  return (
    <div className={cn('lazu-toolbar__group', className)} {...props}>
      {children}
    </div>
  );
}

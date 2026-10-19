import { cva } from 'class-variance-authority';
import React from 'react';
import { cn } from '../../lib/cn';
import type { VariantProps } from 'class-variance-authority';

const badgeVariants = cva('lazu-badge', {
  variants: {
    variant: {
      neutral: 'lazu-badge--neutral',
      success: 'lazu-badge--success',
      warning: 'lazu-badge--warning',
      danger: 'lazu-badge--danger',
      info: 'lazu-badge--info',
    },
  },
  defaultVariants: {
    variant: 'neutral',
  },
});

type BadgeProps = React.HTMLAttributes<HTMLSpanElement> &
  VariantProps<typeof badgeVariants>;

export function Badge({
  className,
  variant = 'neutral',
  ...props
}: BadgeProps) {
  return (
    <span className={cn(badgeVariants({ variant }), className)} {...props} />
  );
}

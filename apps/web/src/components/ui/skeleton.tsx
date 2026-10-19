import React from 'react';
import { cn } from '../../lib/cn';

type SkeletonProps = React.HTMLAttributes<HTMLDivElement> & {
  height?: React.CSSProperties['height'];
  width?: React.CSSProperties['width'];
};

export function Skeleton({
  className,
  height,
  width,
  ...props
}: SkeletonProps) {
  return (
    <div
      aria-hidden='true'
      className={cn('lazu-skeleton', className)}
      style={{ height, width, ...props.style }}
      {...props}
    />
  );
}

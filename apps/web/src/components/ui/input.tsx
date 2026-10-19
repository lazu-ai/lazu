import React from 'react';
import { cn } from '../../lib/cn';

export type InputProps = React.InputHTMLAttributes<HTMLInputElement> & {
  /** Marks the field invalid and turns the border red. */
  error?: boolean;
};

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  (
    { className, error, type = 'text', 'aria-invalid': ariaInvalid, ...props },
    ref,
  ) => (
    <input
      ref={ref}
      type={type}
      aria-invalid={error ? true : ariaInvalid}
      className={cn('lazu-input', className)}
      {...props}
    />
  ),
);

Input.displayName = 'Input';

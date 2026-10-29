import React from 'react';
import { useFieldControlProps } from './field';
import { cn } from '../../lib/cn';

export type InputProps = React.InputHTMLAttributes<HTMLInputElement> & {
  /** Marks the field invalid and turns the border red. */
  error?: boolean;
};

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  (
    {
      className,
      error,
      type = 'text',
      'aria-describedby': ariaDescribedBy,
      'aria-invalid': ariaInvalid,
      ...props
    },
    ref,
  ) => {
    const fieldProps = useFieldControlProps({
      'aria-describedby': ariaDescribedBy,
      'aria-invalid': error ? true : ariaInvalid,
    });
    return (
      <input
        ref={ref}
        type={type}
        {...fieldProps}
        className={cn('lazu-input', className)}
        {...props}
      />
    );
  },
);

Input.displayName = 'Input';

import React from 'react';
import { useFieldControlProps } from './field';
import { cn } from '../../lib/cn';

export type TextareaProps = React.TextareaHTMLAttributes<HTMLTextAreaElement>;

export const Textarea = React.forwardRef<HTMLTextAreaElement, TextareaProps>(
  (
    {
      className,
      'aria-describedby': ariaDescribedBy,
      'aria-invalid': ariaInvalid,
      ...props
    },
    ref,
  ) => {
    const fieldProps = useFieldControlProps({
      'aria-describedby': ariaDescribedBy,
      'aria-invalid': ariaInvalid,
    });
    return (
      <textarea
        ref={ref}
        {...fieldProps}
        className={cn('lazu-textarea', className)}
        {...props}
      />
    );
  },
);

Textarea.displayName = 'Textarea';

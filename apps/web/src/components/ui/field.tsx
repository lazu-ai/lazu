import { XCircle } from 'lucide-react';
import React from 'react';
import { cn } from '../../lib/cn';

type FieldProps = React.HTMLAttributes<HTMLDivElement>;
type LabelProps = React.LabelHTMLAttributes<HTMLLabelElement>;
type FieldMessageProps = React.HTMLAttributes<HTMLDivElement> & {
  error?: boolean;
};

export function Field({ children, className, ...props }: FieldProps) {
  return (
    <div className={cn('lazu-field', className)} {...props}>
      {children}
    </div>
  );
}

export function Label({ children, className, ...props }: LabelProps) {
  return (
    <label className={cn('lazu-label', className)} {...props}>
      {children}
    </label>
  );
}

export function FieldMessage({
  children,
  className,
  error = false,
  ...props
}: FieldMessageProps) {
  if (!children) {
    return null;
  }

  return (
    <div
      className={cn(
        'lazu-field-message',
        error && 'lazu-field-message--error',
        className,
      )}
      role={error ? 'alert' : undefined}
      {...props}
    >
      {error ? (
        <XCircle aria-hidden='true' className='lazu-field-message__icon' />
      ) : null}
      {children}
    </div>
  );
}

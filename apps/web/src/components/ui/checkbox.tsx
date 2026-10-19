import { Checkbox as CheckboxPrimitive } from '@base-ui/react/checkbox';
import { Check, Minus } from 'lucide-react';
import React from 'react';
import { cn } from '../../lib/cn';

type CheckboxProps = CheckboxPrimitive.Root.Props & {
  labelClassName?: string;
};

export const Checkbox = React.forwardRef<HTMLButtonElement, CheckboxProps>(
  (
    { checked, children, className, indeterminate, labelClassName, ...props },
    ref,
  ) => {
    const control = (
      <CheckboxPrimitive.Root
        ref={ref}
        checked={checked}
        className={cn('lazu-checkbox', className)}
        indeterminate={indeterminate}
        {...props}
      >
        <CheckboxPrimitive.Indicator>
          {/* "Some of these": a tri-state parent (a vendor with a few of its
              models ticked) says so with a dash rather than a tick. */}
          {indeterminate ? (
            <Minus size={13} strokeWidth={3} />
          ) : (
            <Check size={13} strokeWidth={3} />
          )}
        </CheckboxPrimitive.Indicator>
      </CheckboxPrimitive.Root>
    );

    if (!children) {
      return control;
    }

    return (
      <label className={cn('lazu-checkbox-label', labelClassName)}>
        {control}
        <span>{children}</span>
      </label>
    );
  },
);

Checkbox.displayName = 'Checkbox';

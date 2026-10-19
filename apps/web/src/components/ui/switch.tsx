import { Switch as SwitchPrimitive } from '@base-ui/react/switch';
import React from 'react';
import { cn } from '../../lib/cn';

type SwitchProps = Omit<SwitchPrimitive.Root.Props, 'onChange'> & {
  labelClassName?: string;
  onChange?: (checked: boolean) => void;
  size?: string;
};

export const Switch = React.forwardRef<HTMLButtonElement, SwitchProps>(
  (
    {
      children,
      className,
      labelClassName,
      onChange,
      onCheckedChange,
      size: _size,
      ...props
    },
    ref,
  ) => {
    const handleCheckedChange: NonNullable<
      SwitchPrimitive.Root.Props['onCheckedChange']
    > = (checked, details) => {
      onCheckedChange?.(checked, details);
      onChange?.(checked);
    };

    const control = (
      <SwitchPrimitive.Root
        ref={ref}
        className={cn('lazu-switch', className)}
        onCheckedChange={handleCheckedChange}
        {...props}
      >
        <SwitchPrimitive.Thumb className='lazu-switch__thumb' />
      </SwitchPrimitive.Root>
    );

    if (!children) {
      return control;
    }

    return (
      <label className={cn('lazu-switch-label', labelClassName)}>
        {control}
        <span>{children}</span>
      </label>
    );
  },
);

Switch.displayName = 'Switch';

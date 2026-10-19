import { Button as BaseButton } from '@base-ui/react/button';
import { cva } from 'class-variance-authority';
import React from 'react';
import { cn } from '../../lib/cn';
import type { VariantProps } from 'class-variance-authority';

const iconButtonVariants = cva('lazu-icon-button', {
  variants: {
    variant: {
      primary: 'lazu-icon-button--primary',
      secondary: 'lazu-icon-button--secondary',
      ghost: 'lazu-icon-button--ghost',
      danger: 'lazu-icon-button--danger',
      success: 'lazu-icon-button--success',
    },
    size: {
      xs: 'lazu-icon-button--xs',
      sm: 'lazu-icon-button--sm',
      md: 'lazu-icon-button--md',
      lg: 'lazu-icon-button--lg',
    },
  },
  defaultVariants: {
    variant: 'ghost',
    size: 'md',
  },
});

export type IconButtonProps = Omit<BaseButton.Props, 'className'> &
  VariantProps<typeof iconButtonVariants> & {
    className?: string;
  };

export const IconButton = React.forwardRef<HTMLElement, IconButtonProps>(
  (
    {
      'aria-label': ariaLabel,
      className,
      size = 'md',
      variant = 'ghost',
      children,
      nativeButton,
      render,
      ...props
    },
    ref,
  ) => {
    const resolvedClassName = cn(
      iconButtonVariants({ variant, size }),
      className,
    );

    if (React.isValidElement(render)) {
      const composedElement = render as React.ReactElement<
        Record<string, unknown>
      >;
      return React.cloneElement(composedElement, {
        ...props,
        ref,
        'aria-label': ariaLabel,
        className: cn(
          typeof composedElement.props.className === 'string'
            ? composedElement.props.className
            : undefined,
          resolvedClassName,
        ),
        children,
      });
    }

    return (
      <BaseButton
        ref={ref}
        render={render}
        nativeButton={nativeButton ?? !render}
        aria-label={ariaLabel}
        className={resolvedClassName}
        {...props}
      >
        {children}
      </BaseButton>
    );
  },
);

IconButton.displayName = 'IconButton';

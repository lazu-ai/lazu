import { Button as BaseButton } from '@base-ui/react/button';
import { cva } from 'class-variance-authority';
import { Check, CircleAlert, LoaderCircle } from 'lucide-react';
import React from 'react';
import { cn } from '../../lib/cn';
import type { VariantProps } from 'class-variance-authority';

export const buttonVariants = cva('lazu-button', {
  variants: {
    variant: {
      primary: 'lazu-button--primary',
      secondary: 'lazu-button--secondary',
      ghost: 'lazu-button--ghost',
      'ghost-danger': 'lazu-button--ghost-danger',
      soft: 'lazu-button--soft',
      'soft-danger': 'lazu-button--soft-danger',
      danger: 'lazu-button--danger',
      success: 'lazu-button--success',
      link: 'lazu-button--link',
    },
    size: {
      xs: 'lazu-button--xs',
      sm: 'lazu-button--sm',
      md: 'lazu-button--md',
      lg: 'lazu-button--lg',
    },
    fullWidth: {
      true: 'lazu-button--full',
      false: null,
    },
  },
  defaultVariants: {
    variant: 'secondary',
    size: 'md',
    fullWidth: false,
  },
});

type ButtonVariantProps = Omit<VariantProps<typeof buttonVariants>, 'size'>;

type ButtonSize =
  | NonNullable<VariantProps<typeof buttonVariants>['size']>
  | 'default'
  | 'small'
  | 'large';

export type ButtonProps = Omit<BaseButton.Props, 'className'> &
  ButtonVariantProps & {
    className?: string;
    errorLabel?: React.ReactNode;
    loading?: boolean;
    loadingLabel?: React.ReactNode;
    size?: ButtonSize;
    status?: 'idle' | 'loading' | 'success' | 'error';
    successLabel?: React.ReactNode;
  };

const normalizeButtonSize = (
  size: ButtonSize | null | undefined,
): VariantProps<typeof buttonVariants>['size'] => {
  if (size === 'default') return 'md';
  if (size === 'small') return 'sm';
  if (size === 'large') return 'lg';
  return size;
};

export const Button = React.forwardRef<HTMLElement, ButtonProps>(
  (
    {
      className,
      disabled,
      errorLabel,
      fullWidth = false,
      loading = false,
      loadingLabel,
      size = 'md',
      status = 'idle',
      successLabel,
      variant = 'secondary',
      children,
      nativeButton,
      render,
      ...props
    },
    ref,
  ) => {
    const normalizedSize = normalizeButtonSize(size);
    const resolvedStatus = loading ? 'loading' : status;
    const stateContent = {
      idle: children,
      /* A button that starts working must not change size while it does.
         Prepending the spinner to the label grew it by the spinner plus a gap
         — 24px — which is enough to re-wrap a toolbar and shove the page below
         it (measured on 用量与日志: the search button's spinner pushed 重置 onto a
         second line and moved everything under the filters by 42px). So the
         spinner sits over the label rather than beside it, and the label keeps
         holding the width. A caller that passes `loadingLabel` means to change
         the words, and gets the old layout. */
      loading: loadingLabel ? (
        <>
          <LoaderCircle
            aria-hidden='true'
            className='lazu-spin-icon'
            size={16}
          />
          {loadingLabel}
        </>
      ) : (
        <span className='lazu-button__working'>
          <LoaderCircle
            aria-hidden='true'
            className='lazu-spin-icon lazu-button__working-spinner'
            size={16}
          />
          <span className='lazu-button__working-label'>{children}</span>
        </span>
      ),
      success: (
        <>
          <Check aria-hidden='true' size={16} />
          {successLabel ?? children}
        </>
      ),
      error: (
        <>
          <CircleAlert aria-hidden='true' size={16} />
          {errorLabel ?? children}
        </>
      ),
    } satisfies Record<typeof resolvedStatus, React.ReactNode>;
    const resolvedClassName = cn(
      buttonVariants({ variant, size: normalizedSize, fullWidth }),
      className,
    );
    const resolvedDisabled = disabled || resolvedStatus === 'loading';

    if (React.isValidElement(render)) {
      const composedElement = render as React.ReactElement<
        Record<string, unknown>
      >;
      const composedOnClick =
        typeof composedElement.props.onClick === 'function'
          ? (composedElement.props
              .onClick as React.MouseEventHandler<HTMLElement>)
          : undefined;
      return React.cloneElement(composedElement, {
        ...props,
        ref,
        'aria-busy': resolvedStatus === 'loading' || undefined,
        'aria-disabled': resolvedDisabled || undefined,
        'aria-live': resolvedStatus === 'idle' ? undefined : 'polite',
        className: cn(
          typeof composedElement.props.className === 'string'
            ? composedElement.props.className
            : undefined,
          resolvedClassName,
        ),
        children: stateContent[resolvedStatus],
        'data-status': resolvedStatus,
        onClick: resolvedDisabled
          ? (event: React.MouseEvent<HTMLElement>) => {
              event.preventDefault();
              event.stopPropagation();
            }
          : ((props.onClick as
              | React.MouseEventHandler<HTMLElement>
              | undefined) ?? composedOnClick),
        tabIndex:
          resolvedDisabled || typeof composedElement.props.tabIndex !== 'number'
            ? resolvedDisabled
              ? -1
              : undefined
            : composedElement.props.tabIndex,
      });
    }

    return (
      <BaseButton
        ref={ref}
        render={render}
        nativeButton={nativeButton ?? !render}
        className={resolvedClassName}
        aria-busy={resolvedStatus === 'loading' || undefined}
        aria-live={resolvedStatus === 'idle' ? undefined : 'polite'}
        data-status={resolvedStatus}
        disabled={resolvedDisabled}
        {...props}
      >
        {stateContent[resolvedStatus]}
      </BaseButton>
    );
  },
);

Button.displayName = 'Button';

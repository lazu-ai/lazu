import { Select as SelectPrimitive } from '@base-ui/react/select';
import { Check, ChevronDown } from 'lucide-react';
import React from 'react';
import { cn } from '../../lib/cn';

type SelectSize = 'sm' | 'md' | 'lg' | (string & {});
type SelectOption = {
  disabled?: boolean;
  label: React.ReactNode;
  value: string;
};

type SelectProps = Omit<
  SelectPrimitive.Root.Props<string, false>,
  'children' | 'items' | 'multiple' | 'onValueChange'
> & {
  'aria-label'?: string;
  align?: 'center' | 'end' | 'start';
  children?: React.ReactNode;
  className?: string;
  contentClassName?: string;
  onValueChange?: (value: string) => void;
  options?: SelectOption[];
  placeholder?: React.ReactNode;
  size?: SelectSize;
  triggerContent?: React.ReactNode;
};

function collectChildItems(children: React.ReactNode): SelectOption[] {
  return React.Children.toArray(children).flatMap((child) => {
    if (!React.isValidElement(child)) return [];
    const props = child.props as {
      children?: React.ReactNode;
      disabled?: boolean;
      value?: unknown;
    };
    if (typeof props.value !== 'string') return [];
    return [
      {
        disabled: props.disabled,
        label: props.children,
        value: props.value,
      },
    ];
  });
}

export function Select({
  'aria-label': ariaLabel,
  align = 'start',
  children,
  className,
  contentClassName,
  onValueChange,
  options = [],
  placeholder,
  size = 'md',
  triggerContent,
  ...props
}: SelectProps) {
  const items = options.length ? options : collectChildItems(children);
  const renderedItems =
    children ??
    options.map((option) => (
      <SelectItem
        key={option.value}
        disabled={option.disabled}
        value={option.value}
      >
        {option.label}
      </SelectItem>
    ));

  return (
    <SelectPrimitive.Root
      items={items}
      onValueChange={(value) => {
        if (value !== null) onValueChange?.(value);
      }}
      {...props}
    >
      <SelectPrimitive.Trigger
        aria-label={ariaLabel}
        className={cn(
          'lazu-select-trigger',
          `lazu-select-trigger--${size}`,
          className,
        )}
      >
        {triggerContent ?? <SelectPrimitive.Value placeholder={placeholder} />}
        <SelectPrimitive.Icon>
          <ChevronDown size={15} />
        </SelectPrimitive.Icon>
      </SelectPrimitive.Trigger>
      <SelectPrimitive.Portal>
        <SelectPrimitive.Positioner
          align={align}
          alignItemWithTrigger={false}
          className='lazu-popup-positioner'
          sideOffset={6}
        >
          <SelectPrimitive.Popup
            className={cn('lazu-select-content', contentClassName)}
          >
            <SelectPrimitive.List>{renderedItems}</SelectPrimitive.List>
          </SelectPrimitive.Popup>
        </SelectPrimitive.Positioner>
      </SelectPrimitive.Portal>
    </SelectPrimitive.Root>
  );
}

export const SelectItem = React.forwardRef<
  HTMLElement,
  SelectPrimitive.Item.Props
>(({ children, className, ...props }, ref) => (
  <SelectPrimitive.Item
    ref={ref}
    className={cn('lazu-select-item', className)}
    {...props}
  >
    <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
    <SelectPrimitive.ItemIndicator>
      <Check size={14} />
    </SelectPrimitive.ItemIndicator>
  </SelectPrimitive.Item>
));

SelectItem.displayName = 'SelectItem';

type MultiSelectValue = string | number;
type MultiSelectOption = {
  disabled?: boolean;
  label: React.ReactNode;
  value: MultiSelectValue;
};

type MultiSelectRenderValue = (
  values: MultiSelectValue[],
  options: MultiSelectOption[],
) => React.ReactNode;

type MultiSelectProps = Omit<
  React.ButtonHTMLAttributes<HTMLButtonElement>,
  'defaultValue' | 'onChange' | 'value'
> & {
  contentClassName?: string;
  defaultValue?: MultiSelectValue[];
  emptyText?: React.ReactNode;
  maxVisible?: number;
  onChange?: (value: MultiSelectValue[]) => void;
  onValueChange?: (value: MultiSelectValue[]) => void;
  options?: MultiSelectOption[];
  placeholder?: React.ReactNode;
  renderValue?: MultiSelectRenderValue;
  size?: SelectSize;
  value?: MultiSelectValue[];
};

export const MultiSelect = React.forwardRef<
  HTMLButtonElement,
  MultiSelectProps
>(
  (
    {
      'aria-label': ariaLabel,
      className,
      contentClassName,
      defaultValue = [],
      disabled,
      emptyText = 'No options',
      maxVisible = 2,
      onChange,
      onValueChange,
      options = [],
      placeholder,
      renderValue,
      size = 'md',
      value,
      ...triggerProps
    },
    ref,
  ) => {
    const [internalValue, setInternalValue] =
      React.useState<MultiSelectValue[]>(defaultValue);
    const selectedValues = value ?? internalValue;
    const selectedOptions = options.filter((option) =>
      selectedValues.includes(option.value),
    );

    const valueLabel = React.useMemo(() => {
      if (renderValue) return renderValue(selectedValues, selectedOptions);
      if (!selectedOptions.length) return placeholder;
      const visibleLabels = selectedOptions
        .slice(0, maxVisible)
        .map((option) => option.label);
      const hiddenCount = selectedOptions.length - visibleLabels.length;
      return hiddenCount > 0 ? (
        <>
          {visibleLabels.map((label, index) => (
            <React.Fragment key={index}>
              {index ? ', ' : null}
              {label}
            </React.Fragment>
          ))}{' '}
          +{hiddenCount}
        </>
      ) : (
        visibleLabels.map((label, index) => (
          <React.Fragment key={index}>
            {index ? ', ' : null}
            {label}
          </React.Fragment>
        ))
      );
    }, [maxVisible, placeholder, renderValue, selectedOptions, selectedValues]);

    return (
      <SelectPrimitive.Root<MultiSelectValue, true>
        defaultValue={defaultValue}
        disabled={disabled}
        items={options}
        multiple
        value={value}
        onValueChange={(nextValue) => {
          if (value === undefined) setInternalValue(nextValue);
          onValueChange?.(nextValue);
          onChange?.(nextValue);
        }}
      >
        <SelectPrimitive.Trigger
          ref={ref}
          aria-label={ariaLabel}
          className={cn(
            'lazu-select-trigger lazu-multi-select-trigger',
            `lazu-select-trigger--${size}`,
            className,
          )}
          {...triggerProps}
        >
          <span
            className={cn(
              'lazu-multi-select-value',
              !selectedOptions.length && 'lazu-multi-select-value--empty',
            )}
          >
            {valueLabel}
          </span>
          <SelectPrimitive.Icon>
            <ChevronDown size={15} />
          </SelectPrimitive.Icon>
        </SelectPrimitive.Trigger>
        <SelectPrimitive.Portal>
          <SelectPrimitive.Positioner
            align='start'
            alignItemWithTrigger={false}
            className='lazu-popup-positioner'
            sideOffset={6}
          >
            <SelectPrimitive.Popup
              className={cn(
                'lazu-select-content lazu-multi-select-content',
                contentClassName,
              )}
            >
              <SelectPrimitive.List className='lazu-multi-select-list'>
                {options.length ? (
                  options.map((option) => (
                    <SelectPrimitive.Item
                      key={String(option.value)}
                      className='lazu-select-item lazu-multi-select-item'
                      disabled={option.disabled}
                      value={option.value}
                    >
                      <span className='lazu-multi-select-check'>
                        <SelectPrimitive.ItemIndicator>
                          <Check size={14} />
                        </SelectPrimitive.ItemIndicator>
                      </span>
                      <SelectPrimitive.ItemText className='lazu-multi-select-option-label'>
                        {option.label}
                      </SelectPrimitive.ItemText>
                    </SelectPrimitive.Item>
                  ))
                ) : (
                  <div className='lazu-multi-select-empty'>{emptyText}</div>
                )}
              </SelectPrimitive.List>
            </SelectPrimitive.Popup>
          </SelectPrimitive.Positioner>
        </SelectPrimitive.Portal>
      </SelectPrimitive.Root>
    );
  },
);

MultiSelect.displayName = 'MultiSelect';

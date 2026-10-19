import { Menu as DropdownMenuPrimitive } from '@base-ui/react/menu';
import React from 'react';
import { cn } from '../../lib/cn';

export const DropdownMenu = DropdownMenuPrimitive.Root;

type DropdownMenuTriggerProps = Omit<
  DropdownMenuPrimitive.Trigger.Props,
  'children' | 'render'
> & {
  children: React.ReactElement;
};

export function DropdownMenuTrigger({
  children,
  ...props
}: DropdownMenuTriggerProps) {
  return <DropdownMenuPrimitive.Trigger render={children} {...props} />;
}

type DropdownMenuContentProps = DropdownMenuPrimitive.Popup.Props &
  Pick<
    DropdownMenuPrimitive.Positioner.Props,
    'align' | 'alignOffset' | 'side' | 'sideOffset'
  >;

export const DropdownMenuContent = React.forwardRef<
  HTMLDivElement,
  DropdownMenuContentProps
>(({ align, alignOffset, className, side, sideOffset = 6, ...props }, ref) => (
  <DropdownMenuPrimitive.Portal>
    <DropdownMenuPrimitive.Positioner
      align={align}
      alignOffset={alignOffset}
      className='lazu-popup-positioner'
      side={side}
      sideOffset={sideOffset}
    >
      <DropdownMenuPrimitive.Popup
        ref={ref}
        className={cn('lazu-dropdown-content', className)}
        {...props}
      />
    </DropdownMenuPrimitive.Positioner>
  </DropdownMenuPrimitive.Portal>
));

DropdownMenuContent.displayName = 'DropdownMenuContent';

type DropdownMenuItemProps = Omit<
  DropdownMenuPrimitive.Item.Props,
  'onSelect'
> & {
  onSelect?: NonNullable<DropdownMenuPrimitive.Item.Props['onClick']>;
};

export const DropdownMenuItem = React.forwardRef<
  HTMLElement,
  DropdownMenuItemProps
>(({ className, onClick, onSelect, ...props }, ref) => (
  <DropdownMenuPrimitive.Item
    ref={ref}
    className={cn('lazu-dropdown-item', className)}
    onClick={(event) => {
      onClick?.(event);
      if (!event.defaultPrevented) onSelect?.(event);
    }}
    {...props}
  />
));

DropdownMenuItem.displayName = 'DropdownMenuItem';

import { Popover as PopoverPrimitive } from '@base-ui/react/popover';
import React from 'react';
import { cn } from '../../lib/cn';

export const Popover = PopoverPrimitive.Root;

type PopoverTriggerProps = Omit<
  PopoverPrimitive.Trigger.Props,
  'children' | 'render'
> & {
  children: React.ReactElement;
};

export const PopoverTrigger = React.forwardRef<
  HTMLButtonElement,
  PopoverTriggerProps
>(({ children, ...props }, ref) => (
  <PopoverPrimitive.Trigger ref={ref} render={children} {...props} />
));

PopoverTrigger.displayName = 'PopoverTrigger';

type PopoverContentProps = PopoverPrimitive.Popup.Props &
  Pick<
    PopoverPrimitive.Positioner.Props,
    'align' | 'alignOffset' | 'side' | 'sideOffset'
  >;

export const PopoverContent = React.forwardRef<
  HTMLDivElement,
  PopoverContentProps
>(({ align, alignOffset, className, side, sideOffset = 6, ...props }, ref) => (
  <PopoverPrimitive.Portal>
    <PopoverPrimitive.Positioner
      align={align}
      alignOffset={alignOffset}
      className='lazu-popup-positioner'
      side={side}
      sideOffset={sideOffset}
    >
      <PopoverPrimitive.Popup
        ref={ref}
        className={cn('lazu-popover-content', className)}
        {...props}
      />
    </PopoverPrimitive.Positioner>
  </PopoverPrimitive.Portal>
));

PopoverContent.displayName = 'PopoverContent';

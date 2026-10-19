import { Tooltip as TooltipPrimitive } from '@base-ui/react/tooltip';
import React from 'react';
import { cn } from '../../lib/cn';

type TooltipProps = {
  children: React.ReactElement;
  /** Extra class on the popup, for the rare tooltip that needs the ink
   *  treatment of the health bars' tip rather than the paper one. */
  className?: string;
  content?: React.ReactNode;
  delayDuration?: TooltipPrimitive.Provider.Props['delay'];
  showArrow?: boolean;
  side?: TooltipPrimitive.Positioner.Props['side'];
};

export function Tooltip({
  children,
  className,
  content,
  delayDuration = 240,
  showArrow: _showArrow,
  side = 'top',
}: TooltipProps) {
  if (!content) {
    return children;
  }

  return (
    <TooltipPrimitive.Provider delay={delayDuration}>
      <TooltipPrimitive.Root>
        <TooltipPrimitive.Trigger render={children} />
        <TooltipPrimitive.Portal>
          <TooltipPrimitive.Positioner
            className='lazu-tooltip-positioner'
            side={side}
            sideOffset={7}
          >
            <TooltipPrimitive.Popup
              className={cn('lazu-tooltip-content', className)}
            >
              {content}
            </TooltipPrimitive.Popup>
          </TooltipPrimitive.Positioner>
        </TooltipPrimitive.Portal>
      </TooltipPrimitive.Root>
    </TooltipPrimitive.Provider>
  );
}

export const TooltipContent = React.forwardRef<
  HTMLDivElement,
  TooltipPrimitive.Popup.Props &
    Pick<TooltipPrimitive.Positioner.Props, 'align' | 'side' | 'sideOffset'>
>(({ align, className, side, sideOffset = 7, ...props }, ref) => (
  <TooltipPrimitive.Positioner
    align={align}
    className='lazu-tooltip-positioner'
    side={side}
    sideOffset={sideOffset}
  >
    <TooltipPrimitive.Popup
      ref={ref}
      className={cn('lazu-tooltip-content', className)}
      {...props}
    />
  </TooltipPrimitive.Positioner>
));

TooltipContent.displayName = 'TooltipContent';

export const TooltipProvider = TooltipPrimitive.Provider;
export const TooltipRoot = TooltipPrimitive.Root;

type TooltipTriggerProps = Omit<
  TooltipPrimitive.Trigger.Props,
  'children' | 'render'
> & {
  children: React.ReactElement;
};

export function TooltipTrigger({ children, ...props }: TooltipTriggerProps) {
  return <TooltipPrimitive.Trigger render={children} {...props} />;
}

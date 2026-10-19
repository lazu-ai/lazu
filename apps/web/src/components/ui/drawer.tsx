import { Dialog as DialogPrimitive } from '@base-ui/react/dialog';
import { X } from 'lucide-react';
import React from 'react';
import { useControlAppearanceClass } from './control-appearance';
import { cn } from '../../lib/cn';

type DrawerProps = Omit<DialogPrimitive.Root.Props, 'children'> & {
  body?: React.ReactNode;
  busy?: boolean;
  children?: React.ReactNode;
  className?: string;
  description?: React.ReactNode;
  footer?: React.ReactNode;
  overlayClassName?: string;
  side?: 'left' | 'right';
  title?: React.ReactNode;
};

export function Drawer({
  body,
  busy = false,
  children,
  className,
  description,
  footer,
  overlayClassName,
  side = 'right',
  title,
  onOpenChange,
  ...props
}: DrawerProps) {
  const controlAppearanceClass = useControlAppearanceClass();
  return (
    <DialogPrimitive.Root
      onOpenChange={(open, details) => {
        if (!open && busy) {
          details.cancel();
          return;
        }
        onOpenChange?.(open, details);
      }}
      {...props}
    >
      {children}
      <DialogPrimitive.Portal>
        <DialogPrimitive.Backdrop
          className={cn('lazu-drawer-overlay', overlayClassName)}
        />
        <DialogPrimitive.Viewport className='lazu-drawer-viewport'>
          <DialogPrimitive.Popup
            className={cn(
              'lazu-drawer-content',
              controlAppearanceClass,
              `lazu-drawer-content--${side}`,
              className,
            )}
            aria-busy={busy || undefined}
          >
            <DialogPrimitive.Close
              className='lazu-drawer-close'
              disabled={busy}
            >
              <X size={17} />
            </DialogPrimitive.Close>
            {title || description ? (
              <div className='lazu-drawer-header'>
                {title ? (
                  <DialogPrimitive.Title className='lazu-drawer-title'>
                    {title}
                  </DialogPrimitive.Title>
                ) : null}
                {description ? (
                  <DialogPrimitive.Description className='lazu-drawer-description'>
                    {description}
                  </DialogPrimitive.Description>
                ) : null}
              </div>
            ) : null}
            <div className='lazu-drawer-body'>{body}</div>
            {footer ? <div className='lazu-drawer-footer'>{footer}</div> : null}
          </DialogPrimitive.Popup>
        </DialogPrimitive.Viewport>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

export const DrawerClose = DialogPrimitive.Close;

type DrawerTriggerProps = Omit<
  DialogPrimitive.Trigger.Props,
  'children' | 'render'
> & {
  children: React.ReactElement;
};

export function DrawerTrigger({ children, ...props }: DrawerTriggerProps) {
  return <DialogPrimitive.Trigger render={children} {...props} />;
}

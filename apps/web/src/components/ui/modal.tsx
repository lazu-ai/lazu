import { Dialog as DialogPrimitive } from '@base-ui/react/dialog';
import { X } from 'lucide-react';
import React from 'react';
import { useControlAppearanceClass } from './control-appearance';
import { cn } from '../../lib/cn';

type ModalProps = Omit<DialogPrimitive.Root.Props, 'children'> & {
  body?: React.ReactNode;
  busy?: boolean;
  children?: React.ReactNode;
  className?: string;
  closeLabel?: string;
  description?: React.ReactNode;
  footer?: React.ReactNode;
  hideClose?: boolean;
  labelledBy?: string;
  frameClasses?: { backdrop: string; viewport: string; popup: string };
  title?: React.ReactNode;
};

type ModalFrameProps = Omit<
  ModalProps,
  'body' | 'title' | 'description' | 'footer'
> & { trigger?: React.ReactNode };

/** Shared dialog shell for layouts whose title and body occupy separate panels. */
export function ModalFrame({
  busy = false,
  children,
  className,
  closeLabel,
  hideClose = false,
  labelledBy,
  frameClasses,
  onOpenChange,
  trigger,
  ...props
}: ModalFrameProps) {
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
      {trigger}
      <DialogPrimitive.Portal>
        <DialogPrimitive.Backdrop
          className={frameClasses?.backdrop ?? 'lazu-modal-overlay'}
        />
        <DialogPrimitive.Viewport
          className={frameClasses?.viewport ?? 'lazu-modal-viewport'}
        >
          <DialogPrimitive.Popup
            className={cn(
              frameClasses?.popup ?? 'lazu-modal-content',
              controlAppearanceClass,
              className,
            )}
            aria-busy={busy || undefined}
            aria-labelledby={labelledBy}
          >
            {!hideClose ? (
              <DialogPrimitive.Close
                aria-label={closeLabel}
                className='lazu-modal-close'
                disabled={busy}
              >
                <X size={17} />
              </DialogPrimitive.Close>
            ) : null}
            {children}
          </DialogPrimitive.Popup>
        </DialogPrimitive.Viewport>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

export const ModalTitle = DialogPrimitive.Title;
export const ModalDescription = DialogPrimitive.Description;

export function Modal({
  body,
  busy = false,
  children,
  className,
  closeLabel,
  description,
  footer,
  hideClose = false,
  title,
  onOpenChange,
  ...props
}: ModalProps) {
  return (
    <ModalFrame
      busy={busy}
      className={className}
      closeLabel={closeLabel}
      hideClose={hideClose}
      onOpenChange={onOpenChange}
      trigger={children}
      {...props}
    >
      {title || description ? (
        <div className='lazu-modal-header'>
          {title ? (
            <ModalTitle className='lazu-modal-title'>{title}</ModalTitle>
          ) : null}
          {description ? (
            <ModalDescription className='lazu-modal-description'>
              {description}
            </ModalDescription>
          ) : null}
        </div>
      ) : null}
      <div className='lazu-modal-body'>{body}</div>
      {footer ? <div className='lazu-modal-footer'>{footer}</div> : null}
    </ModalFrame>
  );
}

export const ModalClose = DialogPrimitive.Close;

type ModalTriggerProps = Omit<
  DialogPrimitive.Trigger.Props,
  'children' | 'render'
> & {
  children: React.ReactElement;
};

export function ModalTrigger({ children, ...props }: ModalTriggerProps) {
  return <DialogPrimitive.Trigger render={children} {...props} />;
}

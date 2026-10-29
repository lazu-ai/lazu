import { Dialog as DialogPrimitive } from '@base-ui/react/dialog';
import { X } from 'lucide-react';
import React, { useEffect, useRef, useState } from 'react';
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
  /** On phones (≤720px) the dialog rises from the bottom as a sheet: a
   *  grabber, a fixed header and footer, and a body that scrolls between
   *  them. `full` keeps it 44px short of the top whatever its content. */
  sheet?: boolean | 'full';
  title?: React.ReactNode;
};

const SHEET_QUERY = '(max-width: 720px)';
const SWIPE_CLOSE_PX = 72;

/* The on-screen keyboard shrinks the visual viewport but not the layout
   one, so a sheet pinned to the bottom would sit behind it. The gap is
   published as a variable the sheet's bottom edge follows. */
function useKeyboardInset(active: boolean) {
  useEffect(() => {
    const viewport = window.visualViewport;
    if (!active || !viewport) return undefined;
    const root = document.documentElement;
    const update = () => {
      const inset = Math.max(
        0,
        window.innerHeight - viewport.height - viewport.offsetTop,
      );
      root.style.setProperty('--lazu-keyboard-inset', `${Math.round(inset)}px`);
    };
    update();
    viewport.addEventListener('resize', update);
    viewport.addEventListener('scroll', update);
    return () => {
      viewport.removeEventListener('resize', update);
      viewport.removeEventListener('scroll', update);
      root.style.removeProperty('--lazu-keyboard-inset');
    };
  }, [active]);
}

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
  sheet = false,
  trigger,
  ...props
}: ModalFrameProps) {
  const controlAppearanceClass = useControlAppearanceClass();
  const popupRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const drag = useRef<{ id: number; y: number } | null>(null);
  useKeyboardInset(Boolean(sheet) && props.open !== false);

  /* Pulling the grabber down past a threshold closes the sheet through its
     close button, so it takes the same path as a tap on 关闭 — busy guards
     and unsaved-change prompts included. */
  const swipe = sheet
    ? {
        onPointerDown: (event: React.PointerEvent<HTMLElement>) => {
          if (busy || !window.matchMedia(SHEET_QUERY).matches) return;
          drag.current = { id: event.pointerId, y: event.clientY };
          event.currentTarget.setPointerCapture(event.pointerId);
        },
        onPointerMove: (event: React.PointerEvent<HTMLElement>) => {
          if (drag.current?.id !== event.pointerId || !popupRef.current) return;
          const dy = Math.max(0, event.clientY - drag.current.y);
          popupRef.current.style.transform = `translateY(${dy}px)`;
        },
        onPointerUp: (event: React.PointerEvent<HTMLElement>) => {
          if (drag.current?.id !== event.pointerId) return;
          const dy = event.clientY - drag.current.y;
          drag.current = null;
          if (popupRef.current) popupRef.current.style.transform = '';
          if (dy > SWIPE_CLOSE_PX) closeRef.current?.click();
        },
        onPointerCancel: () => {
          drag.current = null;
          if (popupRef.current) popupRef.current.style.transform = '';
        },
      }
    : null;

  return (
    <DialogPrimitive.Root
      onOpenChange={(open, details) => {
        if (!open && busy) {
          details.cancel();
          return;
        }
        // A toast sits outside the dialog, so pressing its 撤销 would
        // otherwise count as dismissing the dialog the action came from.
        if (
          !open &&
          details.reason === 'outside-press' &&
          details.event.target instanceof Element &&
          details.event.target.closest('[data-sonner-toaster]')
        ) {
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
            ref={popupRef}
            className={cn(
              frameClasses?.popup ?? 'lazu-modal-content',
              sheet && 'lazu-modal-content--sheet',
              sheet === 'full' && 'lazu-modal-content--sheet-full',
              controlAppearanceClass,
              className,
            )}
            aria-busy={busy || undefined}
            // An explicit undefined would replace the id Base UI wires to
            // ModalTitle, leaving the dialog without a name.
            {...(labelledBy ? { 'aria-labelledby': labelledBy } : {})}
          >
            {swipe ? (
              <span
                aria-hidden='true'
                className='lazu-modal-grabber'
                {...swipe}
              />
            ) : null}
            {!hideClose ? (
              <DialogPrimitive.Close
                ref={closeRef}
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
  sheet = false,
  title,
  onOpenChange,
  ...props
}: ModalProps) {
  // A sheet's header grows a rule once its body scrolls under it.
  const [scrolled, setScrolled] = useState(false);
  return (
    <ModalFrame
      busy={busy}
      className={className}
      closeLabel={closeLabel}
      hideClose={hideClose}
      onOpenChange={onOpenChange}
      sheet={sheet}
      trigger={children}
      {...props}
    >
      {title || description ? (
        <div
          className='lazu-modal-header'
          data-scrolled={(sheet && scrolled) || undefined}
        >
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
      <div
        className='lazu-modal-body'
        onScroll={
          sheet
            ? (event) => setScrolled(event.currentTarget.scrollTop > 0)
            : undefined
        }
      >
        {body}
      </div>
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

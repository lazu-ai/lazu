import { Dialog as DialogPrimitive } from '@base-ui/react/dialog';
import { X } from 'lucide-react';
import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
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

const TABBABLE =
  'input:not([type="hidden"]):not([disabled]), select:not([disabled]), textarea:not([disabled]), button:not([disabled]), a[href], [tabindex]:not([tabindex="-1"]), [contenteditable="true"]';

/**
 * Picks where focus lands when a dialog opens. Base UI's default is the first
 * tabbable element, which here is always the corner close button: one Enter
 * and the dialog is gone. Prefer the first field; otherwise the first other
 * control unless it is destructive; otherwise the popup itself, which still
 * puts Tab one step from the first control. Touch opens keep the default so
 * phones do not raise the keyboard.
 */
export function pickInitialFocus(
  popup: HTMLElement | null,
  openType: string,
): HTMLElement | true {
  if (!popup || openType === 'touch') {
    return true;
  }
  // Focusing something below the fold would scroll a long read-only dialog
  // away from its title, so only take candidates inside the visible frame.
  const frameBottom = popup.getBoundingClientRect().bottom;
  const visible = (node: HTMLElement) =>
    !node.closest('[hidden], [inert]') &&
    node.getClientRects().length > 0 &&
    node.getBoundingClientRect().top < frameBottom;
  const candidates = Array.from(
    popup.querySelectorAll<HTMLElement>(TABBABLE),
  ).filter(
    (node) =>
      visible(node) &&
      !node.classList.contains('lazu-modal-close') &&
      !node.classList.contains('lazu-drawer-close'),
  );
  const field = candidates.find((node) =>
    node.matches(
      'input, select, textarea, [contenteditable="true"], [role="combobox"]',
    ),
  );
  if (field) {
    return field;
  }
  const first = candidates[0];
  if (
    first &&
    !first.matches(
      '.lazu-button--danger, .lazu-button--ghost-danger, .lazu-button--soft-danger',
    )
  ) {
    return first;
  }
  return popup;
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

  const { t } = useTranslation();
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
            // Only override when the caller names its own heading; otherwise
            // Base UI links the popup to its ModalTitle.
            {...(labelledBy ? { 'aria-labelledby': labelledBy } : null)}
            initialFocus={(openType) =>
              pickInitialFocus(popupRef.current, openType)
            }
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
                aria-label={
                  closeLabel ?? t('common.close', { defaultValue: '关闭' })
                }
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

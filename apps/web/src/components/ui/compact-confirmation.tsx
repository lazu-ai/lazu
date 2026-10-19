import { type ReactNode, type Ref, useId } from 'react';
import { Button } from './button';
import './compact-confirmation.css';

type CompactConfirmationProps = {
  title: ReactNode;
  cancelLabel: ReactNode;
  confirmLabel: ReactNode;
  busy?: boolean;
  cancelRef?: Ref<HTMLElement>;
  onCancel: () => void;
  onConfirm: () => void;
};

/** Pair with lazu-compact-confirmation-surface on any popup primitive. */
export function CompactConfirmation({
  title,
  cancelLabel,
  confirmLabel,
  busy = false,
  cancelRef,
  onCancel,
  onConfirm,
}: CompactConfirmationProps) {
  const titleId = useId();
  return (
    <div
      className='lazu-compact-confirmation'
      role='group'
      aria-labelledby={titleId}
      aria-busy={busy}
    >
      <p className='lazu-compact-confirmation__title' id={titleId}>
        {title}
      </p>
      <div className='lazu-compact-confirmation__actions'>
        <Button
          size='xs'
          variant='ghost'
          ref={cancelRef}
          disabled={busy}
          onClick={onCancel}
        >
          {cancelLabel}
        </Button>
        <Button
          size='xs'
          variant='ghost-danger'
          disabled={busy}
          onClick={onConfirm}
        >
          {confirmLabel}
        </Button>
      </div>
    </div>
  );
}

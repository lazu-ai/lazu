import { AlertTriangle, Info } from 'lucide-react';
import { type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Modal } from '../../ui';

type ConfirmDialogProps = {
  cancelText?: ReactNode;
  children?: ReactNode;
  confirmText?: ReactNode;
  description?: ReactNode;
  destructive?: boolean;
  loading?: boolean;
  onCancel?: () => void;
  onConfirm?: () => void;
  open: boolean;
  title?: ReactNode;
};

const ConfirmDialog = ({
  cancelText,
  children,
  confirmText,
  description,
  destructive = false,
  loading = false,
  onCancel,
  onConfirm,
  open,
  title,
}: ConfirmDialogProps) => {
  const { t } = useTranslation();
  const Icon = destructive ? AlertTriangle : Info;

  return (
    <Modal
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen && !loading) onCancel?.();
      }}
      title={title || t('page.confirmDialog.title')}
      description={description}
      className='w-[min(26rem,calc(100vw-2rem))]'
      body={
        <div className='flex gap-3 rounded-lg border border-[var(--ui-border)] bg-[var(--ui-surface-muted)] p-4'>
          <div
            className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-md bg-[var(--ui-surface)] ${
              destructive ? 'text-[var(--ui-danger)]' : 'text-[var(--ui-focus)]'
            }`}
          >
            <Icon aria-hidden='true' size={20} />
          </div>
          <div className='min-w-0 text-sm leading-6 text-[var(--ui-text-muted)]'>
            {children || t('page.confirmDialog.description')}
          </div>
        </div>
      }
      footer={
        <>
          <Button
            disabled={loading}
            type='button'
            variant='ghost'
            onClick={onCancel}
          >
            {cancelText || t('page.confirmDialog.cancel')}
          </Button>
          <Button
            loading={loading}
            type='button'
            variant={destructive ? 'danger' : 'primary'}
            onClick={onConfirm}
          >
            {confirmText || t('page.confirmDialog.confirm')}
          </Button>
        </>
      }
    />
  );
};

export default ConfirmDialog;

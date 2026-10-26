import { useQuery } from '@tanstack/react-query';
import { MoreHorizontal } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from './button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from './dropdown-menu';
import { Field, Label } from './field';
import { InlineNotice } from './inline-notice';
import { Input } from './input';
import { Modal } from './modal';
import { PendingBlock } from './pending';
import { API, isRoot, showError, showSuccess } from '../../helpers';
import { useDelayedReveal } from '../../hooks/common/useDelayedReveal';
import { unwrap } from '../../lib/api-error';

type OpenPaymentDispute = {
  top_up_id: string;
  dispute_id: string;
  project_id: number;
  paid_amount: number;
  disputed_amount: number;
};

const ns = 'component.paymentAdmin';

/** Payment operations available only to site administrators. They sit behind
 *  one 「更多」 menu so the wallet head a customer sees is unchanged. */
export function PaymentAdminTools() {
  const { t } = useTranslation();
  const [disputesOpen, setDisputesOpen] = useState(false);
  const [reconcileOpen, setReconcileOpen] = useState(false);
  const [pendingLoss, setPendingLoss] = useState<OpenPaymentDispute | null>(
    null,
  );
  const [reconcileTopupId, setReconcileTopupId] = useState('');
  const [busy, setBusy] = useState(false);
  const disputes = useQuery({
    queryKey: ['admin-payment-disputes'],
    enabled: isRoot() && disputesOpen,
    queryFn: async (): Promise<OpenPaymentDispute[]> => {
      const data = unwrap<{ items?: OpenPaymentDispute[] }>(
        await API.get('/api/user/payment-disputes', {
          skipErrorHandler: true,
        }),
      );
      return data?.items ?? [];
    },
  });
  const revealDisputesWait = useDelayedReveal(
    disputesOpen && disputes.isPending,
  );

  async function resolveDispute(
    item: OpenPaymentDispute,
    outcome: 'won' | 'lost',
  ) {
    if (busy) return;
    setBusy(true);
    try {
      unwrap(
        await API.post(
          `/api/user/payment-disputes/${encodeURIComponent(item.top_up_id)}/resolve`,
          { dispute_id: item.dispute_id, outcome },
          { skipErrorHandler: true } as never,
        ),
      );
      showSuccess(t(`${ns}.disputeResolved`));
      if (outcome === 'lost') setPendingLoss(null);
      await disputes.refetch();
    } catch (error) {
      showError(error, t(`${ns}.disputeResolveFailed`));
    } finally {
      setBusy(false);
    }
  }

  async function reconcileDocument() {
    const id = reconcileTopupId.trim();
    if (!id || busy) return;
    setBusy(true);
    try {
      unwrap(
        await API.post(
          `/api/user/payments/${encodeURIComponent(id)}/reconcile-documents`,
          {},
          { skipErrorHandler: true } as never,
        ),
      );
      showSuccess(t(`${ns}.reconciled`));
      setReconcileOpen(false);
      setReconcileTopupId('');
    } catch (error) {
      showError(error, t(`${ns}.reconcileFailed`));
    } finally {
      setBusy(false);
    }
  }

  if (!isRoot()) return null;
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger>
          <Button
            aria-label={t(`${ns}.more`)}
            size='sm'
            title={t(`${ns}.more`)}
            variant='secondary'
          >
            <MoreHorizontal aria-hidden='true' size={15} />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align='end'>
          <DropdownMenuItem onSelect={() => setDisputesOpen(true)}>
            {t(`${ns}.disputes`)}
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => setReconcileOpen(true)}>
            {t(`${ns}.reconcile`)}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <Modal
        open={disputesOpen}
        onOpenChange={setDisputesOpen}
        title={t(`${ns}.disputesTitle`)}
        description={t(`${ns}.disputesDescription`)}
        body={
          <div className='grid min-h-[176px] max-h-[70vh] gap-4 overflow-auto'>
            {disputes.isPending && revealDisputesWait ? (
              <PendingBlock height={176} label={t(`${ns}.disputesLoading`)} />
            ) : null}
            {disputes.isError ? (
              <InlineNotice
                tone='warning'
                title={t(`${ns}.disputesLoadFailed`)}
              />
            ) : null}
            {disputes.data?.length === 0 ? (
              <p>{t(`${ns}.disputesEmpty`)}</p>
            ) : null}
            {disputes.data?.map((item) => (
              <div key={item.dispute_id} className='rounded-lg border p-4'>
                <p className='font-semibold'>
                  {t(`${ns}.disputedLine`, {
                    id: item.top_up_id,
                    amount: item.disputed_amount.toFixed(2),
                  })}
                </p>
                <p>
                  {t(`${ns}.originalPayment`, {
                    amount: item.paid_amount.toFixed(2),
                  })}
                </p>
                <p>{t(`${ns}.project`, { id: item.project_id })}</p>
                <a
                  href={`https://dashboard.stripe.com/disputes/${encodeURIComponent(item.dispute_id)}`}
                  target='_blank'
                  rel='noreferrer'
                >
                  {t(`${ns}.viewInStripe`)}
                </a>
                <div className='mt-3 flex gap-2'>
                  <Button
                    disabled={busy}
                    onClick={() => void resolveDispute(item, 'won')}
                  >
                    {t(`${ns}.markWon`)}
                  </Button>
                  <Button disabled={busy} onClick={() => setPendingLoss(item)}>
                    {t(`${ns}.markLost`)}
                  </Button>
                </div>
              </div>
            ))}
          </div>
        }
      />
      <Modal
        open={pendingLoss !== null}
        onOpenChange={(open) => {
          if (!open && !busy) setPendingLoss(null);
        }}
        busy={busy}
        title={t(`${ns}.confirmLostTitle`)}
        description={t(`${ns}.confirmLostDescription`)}
        body={
          pendingLoss ? (
            <div className='grid gap-2'>
              <p>
                {t(`${ns}.paymentReference`, { id: pendingLoss.top_up_id })}
              </p>
              <p>
                {t(`${ns}.disputedAmount`, {
                  amount: pendingLoss.disputed_amount.toFixed(2),
                })}
              </p>
              <p>{t(`${ns}.confirmLostBody`)}</p>
            </div>
          ) : null
        }
        footer={
          <div className='flex justify-end gap-2'>
            <Button
              disabled={busy}
              variant='secondary'
              onClick={() => setPendingLoss(null)}
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              disabled={busy || !pendingLoss}
              variant='danger'
              onClick={() => {
                if (pendingLoss) void resolveDispute(pendingLoss, 'lost');
              }}
            >
              {busy ? t(`${ns}.recording`) : t(`${ns}.confirmLostTitle`)}
            </Button>
          </div>
        }
      />
      <Modal
        open={reconcileOpen}
        onOpenChange={setReconcileOpen}
        title={t(`${ns}.reconcileTitle`)}
        description={t(`${ns}.reconcileDescription`)}
        body={
          <Field>
            <Label htmlFor='reconcile-topup-id'>
              {t(`${ns}.paymentReferenceLabel`)}
            </Label>
            <Input
              id='reconcile-topup-id'
              value={reconcileTopupId}
              onChange={(event) => setReconcileTopupId(event.target.value)}
              placeholder={t(`${ns}.topupIdPlaceholder`)}
            />
          </Field>
        }
        footer={
          <Button
            disabled={!reconcileTopupId.trim() || busy}
            onClick={() => void reconcileDocument()}
          >
            {busy ? t(`${ns}.reconciling`) : t(`${ns}.reconcileAction`)}
          </Button>
        }
      />
    </>
  );
}

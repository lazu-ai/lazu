/* One top-up, shown inside the wallet.
 *
 * The Stripe return, both payment e-mails and the wallet's own history all
 * land on `/console/wallet?top_up_id=…`, and this card is what they land on.
 * Balance credit and document readiness are independent states: the card
 * polls the one order (`/api/user/payments/:id`, which is open to the payer
 * regardless of the projects switch) and lights each part as its real state
 * arrives. A timeout is "still confirming", never "not charged".
 *
 * The same skeleton serves every state; only the unknown parts wait
 * (`PendingValue` in the slot a value will take, the hairline ring beside
 * a document that is not there yet). No decoration: the eyebrow is the
 * machine register (PAID / CONFIRMING / NOT PAID), left untranslated like
 * the paper states' reason phrases. */
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ExternalLink, X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from './button';
import { InlineNotice } from './inline-notice';
import { PendingValue } from './pending';
import { useSession } from '../../context/User';
import { accountPaymentQueryOptions } from '../../features/projects/paymentRecord';
import { API } from '../../helpers/api';
import { useDelayedReveal } from '../../hooks/common/useDelayedReveal';
import { cn } from '../../lib/cn';
import { queryKeys } from '../../lib/query-keys';
import type { TopupRecordData } from '../../features/projects/paymentRecord';

export type { TopupRecordData } from '../../features/projects/paymentRecord';

type TopupRecordProps = {
  topupId: string;
  className?: string;
  /* The document redirect could not resolve a Stripe URL and sent the reader
     back here; say so once, above the buttons they can try again from. */
  documentError?: boolean;
  onClose?: () => void;
};

const POLL_MS = 3_000;
/* After this long the card stops asking Stripe and rests on the e-mail
   promise; the balance and documents still update on the next visit. */
const POLL_BUDGET_S = 120;
/* The "we will e-mail you" sentence appears only once the wait is unusual. */
const MAIL_NOTE_AFTER_S = 60;

const ns = 'page.console.wallet.record';

export function topupDocumentPath(
  projectId: string,
  topupId: string,
  document: 'invoice' | 'invoice_page' | 'receipt',
) {
  const params = new URLSearchParams({
    project_id: projectId,
    top_up_id: topupId,
    document,
    source: 'wallet',
  });
  return `/payment/document?${params.toString()}`;
}

/* Same reading as helpers/render's getMicroUSDPerUSD; kept local so the ui
   barrel does not drag the legacy render helpers (and their alias imports)
   into every consumer. */
function microUSDPerUSD() {
  try {
    const stored = parseFloat(
      localStorage.getItem('micro_usd_per_usd') || '1000000',
    );
    return Number.isFinite(stored) && stored > 0 ? stored : 1_000_000;
  } catch {
    return 1_000_000;
  }
}

function usd(micro: number | undefined | null) {
  const value = (micro ?? 0) / microUSDPerUSD();
  return `$${value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`;
}

function lookupStatus(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') return undefined;
  const response = 'response' in error ? error.response : undefined;
  if (response && typeof response === 'object' && 'status' in response)
    return typeof response.status === 'number' ? response.status : undefined;
  return 'status' in error && typeof error.status === 'number'
    ? error.status
    : undefined;
}

function settled(record: TopupRecordData | undefined) {
  if (!record) return false;
  if (
    record.payment_status === 'expired' ||
    record.payment_status === 'failed' ||
    record.payment_status === 'canceled'
  )
    return true;
  return (
    record.payment_status === 'paid' &&
    record.invoice_status !== 'pending' &&
    (record.invoice_status === 'legacy' ||
      Boolean(record.payment_method && record.payment_method !== 'stripe')) &&
    (record.receipt_ready ||
      record.paid_amount_microusd === 0 ||
      record.invoice_status === 'legacy')
  );
}

export function TopupRecord(props: TopupRecordProps) {
  return <TopupRecordContent key={props.topupId} {...props} />;
}

function TopupRecordContent({
  topupId,
  className,
  documentError = false,
  onClose,
}: TopupRecordProps) {
  const { t, i18n } = useTranslation();
  const userSession = useSession();
  const queryClient = useQueryClient();
  const refreshedPayment = useRef<string | null>(null);
  const [startedAt] = useState(() => Date.now());
  const [elapsed, setElapsed] = useState(0);
  const [switching, setSwitching] = useState(false);

  const payment = useQuery({
    ...accountPaymentQueryOptions(topupId),
    refetchInterval: (query) => {
      const seconds = (Date.now() - startedAt) / 1000;
      if (seconds >= POLL_BUDGET_S) return false;
      if (query.state.error)
        return lookupStatus(query.state.error) === 404 ? false : POLL_MS;
      return settled(query.state.data) ? false : POLL_MS;
    },
  });
  const record = payment.data;
  const exhausted = elapsed >= POLL_BUDGET_S;
  const waiting = !settled(record) && !payment.isError && !exhausted;
  const revealPending = useDelayedReveal(waiting);
  const pendingValue = (chars: number) => (
    <PendingValue
      chars={chars}
      className={cn(
        !revealPending && !exhausted && 'invisible',
        exhausted && '[animation:none]',
      )}
      label={exhausted ? t(`${ns}.unconfirmed`) : undefined}
    />
  );

  useEffect(() => {
    if (
      record?.payment_status !== 'paid' ||
      refreshedPayment.current === record.id
    )
      return;
    refreshedPayment.current = record.id;
    // Account credit affects every project sharing this wallet, plus the legacy wallet.
    for (const queryKey of [
      queryKeys.user,
      queryKeys.dashboard.root,
      ['console', 'wallet'],
      ['projects'],
    ]) {
      void queryClient.invalidateQueries({ queryKey });
    }
  }, [record, queryClient]);

  useEffect(() => {
    if (settled(record) || exhausted || lookupStatus(payment.error) === 404)
      return undefined;
    const timer = window.setInterval(
      () => setElapsed(Math.round((Date.now() - startedAt) / 1000)),
      1_000,
    );
    return () => window.clearInterval(timer);
  }, [record, exhausted, payment.error, startedAt]);

  async function switchAccount() {
    if (switching) return;
    setSwitching(true);
    try {
      await API.get('/api/user/logout');
    } catch {
      // The browser session must still be cleared when the server session expired.
    }
    const next = `${window.location.pathname}${window.location.search}`;
    userSession.signOut();
    localStorage.removeItem('user');
    window.location.replace(`/login?next=${encodeURIComponent(next)}`);
  }

  const closeButton = onClose ? (
    <button
      aria-label={t(`${ns}.close`)}
      className='lazu-topup-record__close'
      onClick={onClose}
      type='button'
    >
      <X aria-hidden='true' size={15} />
    </button>
  ) : null;

  if (payment.isError) {
    const missing = lookupStatus(payment.error) === 404;
    return (
      <section
        className={cn('lazu-topup-record', className)}
        data-state='error'
        role='alert'
      >
        <div className='lazu-topup-record__head'>
          <div className='lazu-topup-record__lead'>
            <span className='lazu-topup-record__eyebrow'>
              {missing ? 'NOT FOUND' : 'UNAVAILABLE'}
            </span>
            <p className='lazu-topup-record__status'>
              {missing ? t(`${ns}.missing`) : t(`${ns}.unavailable`)}
            </p>
            <div className='lazu-topup-record__actions'>
              {missing ? (
                <Button
                  disabled={switching}
                  onClick={() => void switchAccount()}
                  size='sm'
                  variant='primary'
                >
                  {switching ? t(`${ns}.switching`) : t(`${ns}.switchAccount`)}
                </Button>
              ) : (
                <Button
                  onClick={() => void payment.refetch()}
                  size='sm'
                  variant='secondary'
                >
                  {t(`${ns}.retry`)}
                </Button>
              )}
            </div>
          </div>
          {closeButton}
        </div>
      </section>
    );
  }

  const status = record?.payment_status;
  const paid = status === 'paid';
  const notPaid =
    status === 'expired' || status === 'failed' || status === 'canceled';
  const eyebrow = paid
    ? 'PAID'
    : status === 'expired'
      ? 'EXPIRED'
      : status === 'canceled'
        ? 'CANCELED'
        : status === 'failed'
          ? 'NOT PAID'
          : 'CONFIRMING';
  const amount = record
    ? usd(
        paid
          ? record.paid_amount_microusd
          : record.order_amount_microusd || record.credited_microusd,
      )
    : null;
  // `stripe` identifies the checkout processor, not the card or wallet used.
  const methodKey =
    paid && record?.payment_method !== 'stripe'
      ? (record?.payment_method ?? '')
      : '';
  const method = methodKey
    ? t(`${ns}.method.${methodKey}`, { defaultValue: methodKey })
    : '';
  const dateFormat = new Intl.DateTimeFormat(i18n.language, {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
  const billTo = record?.bill_to
    ? [
        record.bill_to.name,
        record.bill_to.address?.city,
        record.bill_to.address?.country,
        record.bill_to.tax_ids?.[0]?.value,
      ]
        .filter(Boolean)
        .join(' · ')
    : '';

  const statusLine = !record ? (
    pendingValue(18)
  ) : paid ? (
    <>
      <span aria-hidden='true' className='lazu-topup-record__dot' />
      {record.project_shared && record.project_name
        ? t(`${ns}.paidProject`, { project: record.project_name })
        : t(`${ns}.paidAccount`)}
    </>
  ) : notPaid ? (
    <>
      <span
        aria-hidden='true'
        className='lazu-topup-record__dot lazu-topup-record__dot--danger'
      />
      {status === 'expired'
        ? t(`${ns}.expired`)
        : status === 'canceled'
          ? t(`${ns}.canceled`)
          : t(`${ns}.failed`)}
    </>
  ) : (
    <>
      {waiting && revealPending ? (
        <span aria-hidden='true' className='lazu-loading__spinner' />
      ) : null}
      {t(`${ns}.confirming`)}
    </>
  );

  const hint = !record
    ? null
    : notPaid
      ? t(`${ns}.failedHint`)
      : paid
        ? null
        : elapsed >= MAIL_NOTE_AFTER_S || elapsed >= POLL_BUDGET_S
          ? `${t(`${ns}.confirmingHint`)} ${t(`${ns}.confirmingMail`)}`
          : t(`${ns}.confirmingHint`);

  const fact = (
    label: string,
    value: React.ReactNode,
    options: { mono?: boolean; pending?: boolean; chars?: number } = {},
  ) => (
    <div className='lazu-topup-record__fact'>
      <dt>{label}</dt>
      <dd className={options.mono ? 'lazu-topup-record__mono' : undefined}>
        {options.pending ? pendingValue(options.chars ?? 10) : value}
      </dd>
    </div>
  );

  const documents = record ? (
    notPaid ? (
      <p className='lazu-topup-record__nodocs'>{t(`${ns}.noDocs`)}</p>
    ) : (
      <div className='lazu-topup-record__docs'>
        <div className='lazu-topup-record__docs-head'>
          <span>{t(`${ns}.docs`)}</span>
          <span>{t(`${ns}.docsHint`)}</span>
        </div>
        <div className='lazu-topup-record__doc'>
          <div className='lazu-topup-record__doc-copy'>
            <span className='lazu-topup-record__doc-name'>
              {t(`${ns}.receipt`)}
            </span>
            <span className='lazu-topup-record__doc-sub'>
              {record.receipt_ready ? (
                t(`${ns}.receiptSub`, { method, amount })
              ) : record.paid_amount_microusd === 0 && paid ? (
                t(`${ns}.receiptNone`)
              ) : record.invoice_status === 'legacy' ? (
                t(`${ns}.receiptLegacy`)
              ) : (
                <>
                  {waiting && revealPending ? (
                    <span
                      aria-hidden='true'
                      className='lazu-loading__spinner lazu-topup-record__ring'
                    />
                  ) : null}
                  {t(`${ns}.receiptPending`)}
                </>
              )}
            </span>
          </div>
          <Button
            disabled={!record.receipt_ready}
            render={
              <a
                aria-label={t(`${ns}.viewReceipt`)}
                href={topupDocumentPath(
                  record.project_id,
                  record.id,
                  'receipt',
                )}
                rel='noreferrer'
                target='_blank'
              />
            }
            size='sm'
            variant='primary'
          >
            <ExternalLink aria-hidden='true' size={14} />
            {t(`${ns}.viewReceipt`)}
          </Button>
        </div>
        <div className='lazu-topup-record__doc'>
          <div className='lazu-topup-record__doc-copy'>
            <span className='lazu-topup-record__doc-name'>
              {t(`${ns}.invoice`)}
              {record.invoice_number ? (
                <span className='lazu-topup-record__mono'>
                  {record.invoice_number}
                </span>
              ) : null}
            </span>
            <span className='lazu-topup-record__doc-sub'>
              {record.invoice_ready ? (
                <>
                  <span aria-hidden='true' className='lazu-topup-record__dot' />
                  {t(`${ns}.invoicePaid`)}
                </>
              ) : record.invoice_status === 'failed' ? (
                t(`${ns}.invoiceFailed`)
              ) : record.invoice_status === 'legacy' ? (
                t(`${ns}.invoiceLegacy`)
              ) : (
                <>
                  {waiting && revealPending ? (
                    <span
                      aria-hidden='true'
                      className='lazu-loading__spinner lazu-topup-record__ring'
                    />
                  ) : null}
                  {paid ? t(`${ns}.invoicePending`) : t(`${ns}.receiptPending`)}
                </>
              )}
            </span>
          </div>
          <Button
            disabled={!record.invoice_ready}
            render={
              <a
                aria-label={t(`${ns}.viewInvoice`)}
                href={topupDocumentPath(
                  record.project_id,
                  record.id,
                  'invoice_page',
                )}
                rel='noreferrer'
                target='_blank'
              />
            }
            size='sm'
            variant='secondary'
          >
            <ExternalLink aria-hidden='true' size={14} />
            {t(`${ns}.viewInvoice`)}
          </Button>
        </div>
        {record.invoice_status !== 'legacy' ? (
          <p className='lazu-topup-record__foot'>{t(`${ns}.foot`)}</p>
        ) : null}
      </div>
    )
  ) : null;

  return (
    <section
      aria-busy={waiting || undefined}
      aria-live='polite'
      className={cn('lazu-topup-record', className)}
      data-state={record ? status : 'loading'}
    >
      {documentError ? (
        <InlineNotice
          className='lazu-topup-record__notice'
          description={t(`${ns}.documentErrorBody`)}
          title={t(`${ns}.documentError`)}
          tone='warning'
        />
      ) : null}
      <div className='lazu-topup-record__head'>
        <div className='lazu-topup-record__lead'>
          <span className='lazu-topup-record__eyebrow'>
            {record ? eyebrow : pendingValue(10)}
          </span>
          <div className='lazu-topup-record__amount'>
            {amount ?? pendingValue(10)}
          </div>
          <p className='lazu-topup-record__status'>{statusLine}</p>
          {hint ? <p className='lazu-topup-record__hint'>{hint}</p> : null}
        </div>
        {closeButton}
      </div>
      <dl className='lazu-topup-record__facts'>
        {fact(
          t(`${ns}.${paid ? 'credited' : 'requestedCredit'}`),
          record ? (
            <>
              {usd(record.credited_microusd)}
              {paid && record.bonus_microusd ? (
                <span className='lazu-topup-record__aside'>
                  {t(`${ns}.creditedBonus`, {
                    amount: usd(record.bonus_microusd),
                  })}
                </span>
              ) : null}
            </>
          ) : null,
          { mono: true, pending: !record, chars: 7 },
        )}
        {fact(
          t(`${ns}.paidOn`),
          record?.paid_at
            ? dateFormat.format(record.paid_at * 1000)
            : t(`${ns}.notPaid`),
          { pending: !record || (!paid && !notPaid), chars: 14 },
        )}
        {fact(t(`${ns}.paymentMethod`), method || '—', {
          pending:
            !record ||
            (!notPaid && !method && record.invoice_status !== 'legacy'),
          chars: 8,
        })}
        {fact(t(`${ns}.reference`), record?.id ?? topupId, { mono: true })}
        {fact(t(`${ns}.invoiceNumber`), record?.invoice_number || '—', {
          mono: true,
          pending: !record || (!notPaid && record.invoice_status === 'pending'),
          chars: 12,
        })}
        {fact(t(`${ns}.billTo`), billTo || '—', {
          pending: !record || (!notPaid && record.invoice_status === 'pending'),
          chars: 16,
        })}
      </dl>
      {exhausted && !settled(record) ? (
        <div className='lazu-topup-record__actions'>
          <Button
            onClick={() => void payment.refetch()}
            size='sm'
            variant='secondary'
          >
            {t(`${ns}.retry`)}
          </Button>
        </div>
      ) : null}
      {documents}
    </section>
  );
}

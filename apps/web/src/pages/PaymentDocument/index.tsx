import { useQuery } from '@tanstack/react-query';
import { useEffect } from 'react';
import { Navigate, useSearchParams } from 'react-router-dom';
import { PendingBlock } from '../../components/ui/pending';
import { loadProjectsClient } from '../../features/projects/client';
import { projectKeys } from '../../features/projects/queryKeys';
import { useDelayedReveal } from '../../hooks/common/useDelayedReveal';

function stripeDocumentURL(value: string | null | undefined) {
  if (!value) return null;
  try {
    const url = new URL(value);
    if (
      url.protocol === 'https:' &&
      (url.hostname === 'pay.stripe.com' ||
        url.hostname === 'invoice.stripe.com')
    )
      return url.href;
  } catch {
    return null;
  }
  return null;
}

/** Compatibility route for old email links and a transient Stripe resolver.
 * All user-facing payment and error states live in the account wallet. */
export default function PaymentDocument() {
  const [params] = useSearchParams();
  const projectId = params.get('project_id') || '';
  const topupId = params.get('top_up_id') || '';
  const document = params.get('document');
  const known =
    document === 'invoice' ||
    document === 'invoice_page' ||
    document === 'receipt';
  const valid = Boolean(projectId && topupId && known);
  const walletParams = new URLSearchParams();
  if (projectId) walletParams.set('project_id', projectId);
  if (topupId) walletParams.set('top_up_id', topupId);
  if (known) walletParams.set('document', document);

  const query = useQuery({
    queryKey: [...projectKeys.topup(projectId, topupId), 'document', document],
    queryFn: async () =>
      (await loadProjectsClient()).getTopupDocument(
        projectId,
        topupId,
        document as 'invoice' | 'invoice_page' | 'receipt',
      ),
    enabled: valid,
    retry: false,
  });
  const destination = stripeDocumentURL(query.data?.url);
  const showPending = useDelayedReveal(valid && query.isPending);
  useEffect(() => {
    if (destination) window.location.replace(destination);
  }, [destination]);

  if (!valid)
    return (
      <Navigate
        replace
        to={`/console/wallet${walletParams.size ? `?${walletParams}` : ''}`}
      />
    );
  if (query.isError || (query.isSuccess && !destination)) {
    walletParams.set('document_error', 'unavailable');
    return <Navigate replace to={`/console/wallet?${walletParams}`} />;
  }
  return (
    <main
      aria-busy={query.isPending}
      className='mx-auto min-h-screen w-full max-w-4xl p-4'
    >
      {query.isPending && showPending ? (
        <PendingBlock
          height='calc(100vh - 2rem)'
          label='Opening payment document'
        />
      ) : null}
    </main>
  );
}

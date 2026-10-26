import { useTranslation } from 'react-i18next';
import { Link, Navigate, useSearchParams } from 'react-router-dom';
import { Button, StatusPage } from '../../components/ui';

type PaymentReturnProps = {
  status?: 'success' | 'fail' | string;
};

const PaymentReturn = ({ status = 'success' }: PaymentReturnProps) => {
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();
  const projectTopupId = searchParams.get('top_up_id') || '';
  const isSuccess = status === 'success';
  /* A project top-up returns into the wallet, where the one record card
   * confirms, credits and lights its documents in place. The Stripe success
   * URL keeps pointing here so in-flight sessions still land. */
  if (projectTopupId) {
    const next = new URLSearchParams({ top_up_id: projectTopupId });
    const projectId = searchParams.get('project_id');
    if (projectId) next.set('project_id', projectId);
    return <Navigate replace to={`/console/wallet?${next.toString()}`} />;
  }
  const tradeNo =
    searchParams.get('trade_no') || searchParams.get('session_id');
  const reason = searchParams.get('reason') || searchParams.get('message');

  /* One fact to match this page to the payment: why it stopped, when the
   * gateway said, otherwise the reference the reader can quote to support. */
  const pill =
    !isSuccess && reason
      ? `${t('page.payment.reason')} · ${reason}`
      : tradeNo
        ? `${t('page.payment.tradeNo')} ${tradeNo}`
        : undefined;

  return (
    <StatusPage
      action={
        <Button render={<Link to='/console/wallet' />} variant='primary'>
          {t('page.payment.walletAction')}
        </Button>
      }
      description={
        isSuccess
          ? t('page.payment.success.description')
          : t('page.payment.fail.description')
      }
      ground
      mark={isSuccess ? 'Paid' : 'Unpaid'}
      pill={pill}
      secondaryAction={
        isSuccess ? (
          <Button
            render={<Link to='/console/usage?tab=logs' />}
            variant='ghost'
          >
            {t('page.payment.logsAction')}
          </Button>
        ) : undefined
      }
      title={
        isSuccess
          ? t('page.payment.success.title')
          : t('page.payment.fail.title')
      }
    />
  );
};

export default PaymentReturn;

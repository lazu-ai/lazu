import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { History } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { isDayKey, isMonthKey } from './billMonths';
import { RootTopupHistory } from './RootTopupHistory';
import { TopUpCard, type LowBalanceMail, type PayMethod } from './TopUpCard';
import { TopupRecordsDialog, WalletBill } from './WalletBill';
import { Button } from '../../components/ui/button';
import ConsolePage from '../../components/ui/console-page';
import { PaymentAdminTools } from '../../components/ui/payment-admin-tools';
import { Tabs, TabsList, TabsTrigger } from '../../components/ui/tabs';
import {
  TOPUP_MAX_DEFAULT,
  TOPUP_MIN_DEFAULT,
} from '../../components/ui/topup-bar';
import { useStatus } from '../../context/Status';
import { useSession } from '../../context/User';
import { useTopUpOffer, webExtensions } from '../../extensions';
import { API } from '../../helpers/api';
import { getSupportEmail } from '../../helpers/brandUrls';
import { asMicroUSD } from '../../helpers/money';
import { getMicroUSDPerUSD, microUSDToUSD } from '../../helpers/render';
import { isRoot } from '../../helpers/utils';
import { useSelfQuery } from '../../hooks/common/useSelfQuery';
import { apiErrorHandler, unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import './wallet.css';

const ns = 'page.console.wallet';
const topupNs = 'page.console.topup';

type TopupSource = {
  enable_stripe_topup?: boolean;
  low_balance_mail?: {
    available?: boolean;
    on?: boolean;
    threshold_microusd?: number;
  };
  pay_methods?: PayMethod[];
  stripe_max_topup?: number;
  stripe_min_topup?: number;
};

/** The amount the reader last took to checkout. It is deliberately not "the
 *  last top-up": a created checkout may be abandoned, and this page never
 *  learns the outcome, so it remembers the choice rather than claiming a
 *  completed payment. */
const LAST_AMOUNT_STORAGE_KEY = 'lazu:wallet:last-selected-amount';

/** Reading a stored amount is a convenience, never a requirement: private
 *  windows and cleared storage must land on the same page. */
const readLastAmount = (minimum: number, maximum: number) => {
  try {
    const stored = Number(localStorage.getItem(LAST_AMOUNT_STORAGE_KEY));
    if (
      Number.isSafeInteger(stored) &&
      stored > 0 &&
      stored >= minimum &&
      stored <= maximum
    )
      return stored;
  } catch {
    /* storage unavailable — fall through to the default */
  }
  return undefined;
};

const normalizeTopupInfo = (
  status: TopupSource | null | undefined,
  data: TopupSource | null | undefined,
) => {
  const enableStripeTopUp =
    data?.enable_stripe_topup ?? status?.enable_stripe_topup ?? false;
  /* The server's range (operator floor, fixed ceiling); the defaults cover a
     status that has not loaded, and the pay endpoint refuses outside its own. */
  const minTopUp =
    Number(data?.stripe_min_topup) ||
    Number(status?.stripe_min_topup) ||
    TOPUP_MIN_DEFAULT;
  const maxTopUp =
    Number(data?.stripe_max_topup) ||
    Number(status?.stripe_max_topup) ||
    TOPUP_MAX_DEFAULT;
  const mail = data?.low_balance_mail;
  const lowBalance: LowBalanceMail | null = mail
    ? {
        available: Boolean(mail.available),
        on: mail.on !== false,
        thresholdUSD:
          Number(mail.threshold_microusd || 0) / getMicroUSDPerUSD(),
      }
    : null;

  return {
    enableStripeTopUp,
    lowBalance,
    maxTopUp,
    minTopUp,
    payMethods:
      Array.isArray(data?.pay_methods) && data.pay_methods.length > 0
        ? data.pay_methods
        : enableStripeTopUp
          ? [{ name: 'Stripe', status: 'available', type: 'stripe' }]
          : [],
  };
};

type WalletTab = 'bill' | 'topup';

const WalletPage = () => {
  const { t } = useTranslation();
  const [searchParams, setSearchParams] = useSearchParams();
  const queryClient = useQueryClient();
  const userSession = useSession();
  const statusState = useStatus();
  const [selectedMethod, setSelectedMethod] = useState('stripe');

  /* The view lives in the address so a month or a day can be linked, and so
     the Stripe return and payment e-mails (?top_up_id=) land on the page
     with 充值记录 open over it, that payment opened. */
  const tab: WalletTab = searchParams.get('tab') === 'bill' ? 'bill' : 'topup';
  const monthParam = searchParams.get('month');
  const dayParam = searchParams.get('day');
  const month = isMonthKey(monthParam) ? monthParam : null;
  const day = isDayKey(dayParam) ? dayParam : null;
  const recordId = searchParams.get('top_up_id')?.trim() || '';
  const updateParams = (
    changes: Record<string, string | null>,
    replace = false,
  ) => {
    const next = new URLSearchParams(searchParams);
    for (const [key, value] of Object.entries(changes)) {
      if (value === null) next.delete(key);
      else next.set(key, value);
    }
    setSearchParams(next, { replace });
  };
  const closeRecord = () =>
    updateParams(
      {
        document: null,
        document_error: null,
        project_id: null,
        top_up_id: null,
      },
      true,
    );

  const ownerParam = isRoot() ? searchParams.get('owner')?.trim() || '' : '';
  const [historyOpen, setHistoryOpen] = useState(Boolean(ownerParam));
  useEffect(() => {
    setHistoryOpen(Boolean(ownerParam));
  }, [ownerParam]);

  /* "Today" for the bill, fixed for the visit so the months and their
     ranges do not shift under the reader at midnight. */
  const [now] = useState(() => dayjs());

  const userQuery = useSelfQuery(Boolean(userSession.user?.id));
  useEffect(() => {
    const data = userQuery.data;
    if (data) {
      userSession.signIn(data);
    }
  }, [userQuery.data, userSession.signIn]);

  const topupInfoQuery = useQuery({
    queryKey: [...queryKeys.console.detail('wallet', 'info')],
    queryFn: async () => {
      const res = await API.get('/api/user/topup/info');
      return normalizeTopupInfo(statusState?.status, unwrap(res) ?? {});
    },
  });

  const paymentMutation = useMutation<
    string | undefined,
    Error,
    { amount: number; method: string }
  >({
    mutationFn: async ({ amount, method }) => {
      const res = await API.post('/api/user/stripe/pay', {
        amount: Math.trunc(amount),
        payment_method: method,
      });
      return unwrap<{ pay_link?: string } | null>(res)?.pay_link;
    },
    onSuccess: (payLink, { amount }) => {
      try {
        // Only once the checkout actually exists, and only as the amount the
        // reader chose — a rejected request must not become their default.
        localStorage.setItem(
          LAST_AMOUNT_STORAGE_KEY,
          String(Math.trunc(amount)),
        );
      } catch {
        /* remembering the amount is a convenience, never a precondition */
      }
      if (payLink) window.open(payLink, '_blank');
    },
    onError: apiErrorHandler,
  });

  const infoKey = queryKeys.console.detail('wallet', 'info');
  const lowBalanceMutation = useMutation<void, Error, boolean>({
    mutationFn: async (on) => {
      unwrap(await API.put('/api/user/self/mail-prefs', { low_balance: on }));
    },
    onMutate: (on) => {
      queryClient.setQueryData(
        infoKey,
        (current: ReturnType<typeof normalizeTopupInfo> | undefined) =>
          current?.lowBalance
            ? { ...current, lowBalance: { ...current.lowBalance, on } }
            : current,
      );
    },
    onError: (error) => {
      void queryClient.invalidateQueries({ queryKey: infoKey });
      apiErrorHandler(error);
    },
  });

  const info =
    topupInfoQuery.data || normalizeTopupInfo(statusState?.status, {});
  const user = userQuery.data || userSession.user;
  const walletBalance =
    user?.quota === undefined || user?.quota === null
      ? null
      : microUSDToUSD(asMicroUSD(user.quota));
  const balancePending =
    userQuery.isPending || (userQuery.isFetching && walletBalance === null);
  /* No configured payment method means this deployment does not sell credit;
     the 充值 tab then says so and offers the way to get it switched on. */
  const canRecharge = info.enableStripeTopUp && info.payMethods.length > 0;
  const firstTopup = useTopUpOffer(canRecharge);
  const rememberedAmount = useMemo(
    () => readLastAmount(info.minTopUp, info.maxTopUp),
    [info.minTopUp, info.maxTopUp],
  );

  const actions = isRoot() ? (
    <>
      <Button onClick={() => setHistoryOpen(true)} variant='secondary'>
        <History aria-hidden='true' size={14} />
        {t(`${topupNs}.action.history`)}
      </Button>
      <PaymentAdminTools />
    </>
  ) : null;

  return (
    <ConsolePage
      appearance='review'
      title={t(`${ns}.title`, { defaultValue: '钱包' })}
      actions={actions}
      className='wallet-page'
      tabs={
        <Tabs
          value={tab}
          onValueChange={(value) =>
            updateParams({
              day: null,
              month: null,
              tab: value === 'bill' ? 'bill' : null,
            })
          }
        >
          <TabsList aria-label={t(`${ns}.tabs.label`)}>
            <TabsTrigger value='topup'>{t(`${ns}.tabs.topup`)}</TabsTrigger>
            <TabsTrigger value='bill'>{t(`${ns}.tabs.bill`)}</TabsTrigger>
          </TabsList>
        </Tabs>
      }
    >
      {tab === 'topup' ? (
        <div className='wal-body wal-body--topup'>
          {canRecharge ? (
            <TopUpCard
              balancePending={balancePending}
              balanceUSD={walletBalance}
              bonusLabel={firstTopup.bonusLabel}
              bonusUSD={firstTopup.bonusUSD}
              key={`${info.minTopUp}-${info.maxTopUp}`}
              lowBalance={info.lowBalance}
              lowBalanceSaving={lowBalanceMutation.isPending}
              max={info.maxTopUp}
              method={selectedMethod}
              methods={info.payMethods}
              min={info.minTopUp}
              note={firstTopup.note}
              onLowBalanceChange={(on) => lowBalanceMutation.mutate(on)}
              onMethodChange={setSelectedMethod}
              onPay={(amount) =>
                paymentMutation.mutate({ amount, method: selectedMethod })
              }
              paying={paymentMutation.isPending}
              rememberedAmount={rememberedAmount}
            />
          ) : (
            <section className='wal-card wal-topup-empty'>
              <strong>
                {t(`${topupNs}.recharge.unavailableTitle`, {
                  defaultValue: '在线充值还没开通',
                })}
              </strong>
              <p>
                {t(`${topupNs}.recharge.unavailableBody`, {
                  defaultValue:
                    '这个站点还没有配置支付方式。联系我们，确认后会为你的账户开通，余额照常可用。',
                })}
              </p>
              <Button
                nativeButton={false}
                render={
                  <a
                    aria-label={t(`${topupNs}.recharge.contact`, {
                      defaultValue: '联系我们开通',
                    })}
                    href={`mailto:${getSupportEmail()}`}
                  />
                }
                variant='secondary'
              >
                {t(`${topupNs}.recharge.contact`, {
                  defaultValue: '联系我们开通',
                })}
              </Button>
            </section>
          )}
          {webExtensions().map((extension) =>
            extension.WalletAside ? (
              <extension.WalletAside
                className='wal-aside'
                key={extension.name}
              />
            ) : null,
          )}
        </div>
      ) : (
        <div className='wal-body'>
          <WalletBill
            balancePending={balancePending}
            balanceUSD={walletBalance}
            day={day}
            month={month}
            now={now}
            onDay={(next) => updateParams({ day: next })}
            onMonth={(next) => updateParams({ day: null, month: next })}
          />
        </div>
      )}
      {recordId ? (
        <TopupRecordsDialog
          documentError={Boolean(searchParams.get('document_error'))}
          key={recordId}
          onClose={closeRecord}
          topupId={recordId}
        />
      ) : null}
      {isRoot() ? (
        <RootTopupHistory
          key={ownerParam}
          onOpenChange={setHistoryOpen}
          open={historyOpen}
          owner={ownerParam}
        />
      ) : null}
    </ConsolePage>
  );
};

export default WalletPage;

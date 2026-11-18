import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { Download, Search } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useSearchParams } from 'react-router-dom';
import {
  resolveTopupCreditUSD,
  resolveTopupEffectiveTimestamp,
  resolveTopupPaidUSD,
} from './walletAmounts';
import {
  buildDailyWalletLedger,
  reconcileWalletStatement,
  resolveWalletRouteState,
  type WalletBillingView,
  type WalletLedgerDay,
  type WalletTab,
} from './walletLedger';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import ConsolePage from '../../components/ui/console-page';
import { DataTableShell } from '../../components/ui/data-table-shell';
import { EmptyState } from '../../components/ui/empty-state';
import { FieldMessage } from '../../components/ui/field';
import { InlineNotice } from '../../components/ui/inline-notice';
import { Input } from '../../components/ui/input';
import { Modal } from '../../components/ui/modal';
import { Pagination } from '../../components/ui/pagination';
import { PaymentAdminTools } from '../../components/ui/payment-admin-tools';
import { PendingBlock, PendingValue } from '../../components/ui/pending';
import { Tabs, TabsList, TabsTrigger } from '../../components/ui/tabs';
import { notify } from '../../components/ui/toast';
import {
  TOPUP_DEFAULT,
  TOPUP_MAX_DEFAULT,
  TOPUP_MIN_DEFAULT,
  TopupBar,
} from '../../components/ui/topup-bar';
import { TopupRecord } from '../../components/ui/topup-record';
import { useStatus } from '../../context/Status';
import { useSession } from '../../context/User';
import { useTopUpOffer, webExtensions } from '../../extensions';
import { API } from '../../helpers/api';
import { getSupportEmail } from '../../helpers/brandUrls';
import { asMicroUSD } from '../../helpers/money';
import { getMicroUSDPerUSD, microUSDToUSD } from '../../helpers/render';
import { isRoot, showError } from '../../helpers/utils';
import { useDelayedReveal } from '../../hooks/common/useDelayedReveal';
import { useSelfQuery } from '../../hooks/common/useSelfQuery';
import { apiErrorHandler, errorMessage, unwrap } from '../../lib/api-error';
import { formatTimestamp } from '../../lib/date';
import { queryKeys } from '../../lib/query-keys';
import './wallet.css';
import type { ColumnDef } from '@tanstack/react-table';
import type { ChangeEvent } from 'react';

const ns = 'page.console.wallet';
const topupNs = 'page.console.topup';
const billingNs = 'page.console.billing';

type PayMethod = {
  name: string;
  status?: string;
  type: string;
};

type TopupSource = {
  enable_stripe_topup?: boolean;
  pay_methods?: PayMethod[];
  stripe_max_topup?: number;
  stripe_min_topup?: number;
};

type DashboardUsageData = {
  totals?: {
    amount_microusd?: number;
  };
};

type TopupOrderRecord = Record<string, unknown> & {
  amount?: number;
  complete_time?: number;
  credited_microusd?: number;
  create_time?: number;
  id?: number | string;
  money?: number;
  paid_amount?: number;
  payment_method?: string;
  status?: string;
  trade_no?: string;
};

type WalletLedgerData = {
  items?: WalletLedgerDay[];
  opening_balance_microusd?: number;
};

const STATUS_VARIANTS: Record<string, 'danger' | 'success' | 'warning'> = {
  expired: 'danger',
  pending: 'warning',
  success: 'success',
};

const DEFAULT_ALERT_THRESHOLD_USD = 0.5;

const toNumber = (value: unknown) => {
  const parsed = Number(value || 0);
  return Number.isFinite(parsed) ? parsed : 0;
};

const formatCurrency = (value: number) =>
  value.toLocaleString(undefined, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });

const formatSigned = (value: number) =>
  `${value < 0 ? '−$' : '+$'}${formatCurrency(Math.abs(value))}`;

const LAST_AMOUNT_STORAGE_KEY = 'lazu:wallet:last-selected-amount';

const readLastAmount = (minimum: number) => {
  try {
    const stored = Number(localStorage.getItem(LAST_AMOUNT_STORAGE_KEY));
    if (Number.isFinite(stored) && stored > 0 && stored >= minimum)
      return stored;
  } catch {
    /* storage unavailable — fall through to the default */
  }
  return undefined;
};

const getSearchableHistoryKeyword = (value: unknown) => {
  const trimmed = String(value || '').trim();
  const effective = trimmed.replace(/[%_!]/g, '').trim();
  return effective.length >= 2 ? trimmed : '';
};

const parseAlertThresholdUSD = (rawSetting: unknown): number | null => {
  if (typeof rawSetting !== 'string' || !rawSetting.trim()) {
    return DEFAULT_ALERT_THRESHOLD_USD;
  }
  try {
    const parsed = JSON.parse(rawSetting) as {
      quota_warning_threshold?: unknown;
    };
    if (parsed.quota_warning_threshold == null) {
      return DEFAULT_ALERT_THRESHOLD_USD;
    }
    const usd = Number(parsed.quota_warning_threshold) / getMicroUSDPerUSD();
    return Number.isFinite(usd) ? usd : null;
  } catch {
    return null;
  }
};

const normalizeTopupInfo = (
  status: TopupSource | null | undefined,
  data: TopupSource | null | undefined,
) => {
  const enableStripeTopUp =
    data?.enable_stripe_topup ?? status?.enable_stripe_topup ?? false;
  const minTopUp =
    Number(data?.stripe_min_topup) ||
    Number(status?.stripe_min_topup) ||
    TOPUP_MIN_DEFAULT;
  const maxTopUp =
    Number(data?.stripe_max_topup) ||
    Number(status?.stripe_max_topup) ||
    TOPUP_MAX_DEFAULT;

  return {
    enableStripeTopUp,
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

const WalletPage = () => {
  const { t } = useTranslation();
  const [searchParams, setSearchParams] = useSearchParams();
  const recordId = searchParams.get('top_up_id')?.trim() || '';
  const closeRecord = () => {
    const next = new URLSearchParams(searchParams);
    for (const key of ['top_up_id', 'project_id', 'document', 'document_error'])
      next.delete(key);
    setSearchParams(next, { replace: true });
  };

  const queryClient = useQueryClient();
  const userSession = useSession();
  const statusState = useStatus();
  const rootUser = isRoot();
  const routeState = resolveWalletRouteState(searchParams, rootUser);
  const activeTab = routeState.tab;
  const billingView = routeState.billingView;
  const ownerParam = routeState.ownerParam;

  const [period, setPeriod] = useState<'current' | 'previous'>('current');
  const [selectedMethod, setSelectedMethod] = useState('stripe');
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [amount, setAmount] = useState(0);
  const [completeTradeNo, setCompleteTradeNo] = useState<string | null>(null);
  const [historyPage, setHistoryPage] = useState(1);
  const [historyPageSize, setHistoryPageSize] = useState(10);
  const [historyKeyword, setHistoryKeyword] = useState('');
  const [historyOwner, setHistoryOwner] = useState(ownerParam);

  useEffect(() => {
    setHistoryOwner(ownerParam);
    setHistoryKeyword('');
    setHistoryPage(1);
  }, [ownerParam]);

  const handleTabChange = (value: string) => {
    const nextTab: WalletTab = value === 'billing' ? 'billing' : 'topup';
    const next = new URLSearchParams(searchParams);
    if (nextTab === 'topup') {
      next.delete('tab');
      next.delete('view');
    } else {
      next.set('tab', 'billing');
    }
    setSearchParams(next, { replace: true });
  };

  const handleBillingViewChange = (value: string) => {
    const nextView: WalletBillingView = value === 'orders' ? 'orders' : 'daily';
    const next = new URLSearchParams(searchParams);
    next.set('tab', 'billing');
    if (nextView === 'orders') {
      next.set('view', 'orders');
    } else {
      next.delete('view');
    }
    setSearchParams(next, { replace: true });
  };

  const anchor = period === 'current' ? dayjs() : dayjs().subtract(1, 'month');
  const rangeFrom = anchor.startOf('month');
  const rangeTo =
    period === 'current' ? dayjs().endOf('day') : anchor.endOf('month');
  const startTimestamp = rangeFrom.unix();
  const endTimestamp = rangeTo.unix();

  const recent7dStart = useMemo(
    () => dayjs().subtract(6, 'day').startOf('day').unix(),
    [],
  );
  const recent7dEnd = useMemo(() => dayjs().endOf('day').unix(), []);

  const userQuery = useSelfQuery(Boolean(userSession.user?.id));
  useEffect(() => {
    const data = userQuery.data;
    if (data) {
      userSession.signIn(data);
    }
  }, [userQuery.data, userSession.signIn]);

  const recentUsageQuery = useQuery({
    queryKey: [
      ...queryKeys.dashboard.summary(),
      'wallet-runway-7d',
      recent7dStart,
      recent7dEnd,
    ],
    enabled: activeTab === 'topup',
    queryFn: async () => {
      const res = await API.get('/api/data/dashboard/self', {
        params: {
          start_timestamp: recent7dStart,
          end_timestamp: recent7dEnd,
        },
        skipErrorHandler: true,
      });
      return (unwrap<DashboardUsageData | null>(res) ||
        {}) as DashboardUsageData;
    },
  });

  const topupInfoQuery = useQuery({
    queryKey: [...queryKeys.console.detail('wallet', 'info')],
    enabled: activeTab === 'topup',
    queryFn: async () => {
      const res = await API.get('/api/user/topup/info');
      return normalizeTopupInfo(statusState?.status, unwrap(res) ?? {});
    },
  });

  const browserTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone;

  const walletLedgerQuery = useQuery({
    queryKey: [
      ...queryKeys.console.detail('wallet', 'ledger'),
      startTimestamp,
      endTimestamp,
      browserTimezone,
    ],
    enabled: activeTab === 'billing',
    queryFn: async () => {
      const res = await API.get('/api/user/wallet/ledger', {
        params: {
          start_timestamp: startTimestamp,
          end_timestamp: rangeTo.add(1, 'second').unix(),
          timezone: browserTimezone,
          timezone_offset_minutes: new Date().getTimezoneOffset(),
        },
      });
      return (unwrap<WalletLedgerData>(res) || {}) as WalletLedgerData;
    },
  });

  const historySearchKeyword = getSearchableHistoryKeyword(historyKeyword);
  const historyKeywordTooShort =
    historyKeyword.trim().length > 0 && !historySearchKeyword;

  const historyQuery = useQuery({
    queryKey: [
      ...queryKeys.console.detail('wallet', 'history'),
      historyPage,
      historyPageSize,
      historySearchKeyword,
      historyOwner,
    ],
    enabled: activeTab === 'billing',
    queryFn: async () => {
      const base = rootUser ? '/api/user/topup' : '/api/user/topup/self';
      const res = await API.get(base, {
        params: {
          p: historyPage,
          page_size: historyPageSize,
          ...(historySearchKeyword ? { keyword: historySearchKeyword } : {}),
          ...(historyOwner ? { owner: historyOwner } : {}),
        },
        skipErrorHandler: true,
      });
      const data = unwrap<{ items?: unknown; total?: number }>(res);
      return {
        items: (Array.isArray(data?.items)
          ? data.items
          : []) as TopupOrderRecord[],
        total: Number(data?.total || 0),
      };
    },
  });

  const paymentMutation = useMutation<
    string | undefined,
    Error,
    { amount: number; method: string }
  >({
    mutationFn: async ({ amount: payAmount, method }) => {
      const res = await API.post('/api/user/stripe/pay', {
        amount: Math.trunc(payAmount),
        payment_method: method,
      });
      return unwrap<{ pay_link?: string } | null>(res)?.pay_link;
    },
    onSuccess: (payLink, { amount: payAmount }) => {
      try {
        localStorage.setItem(
          LAST_AMOUNT_STORAGE_KEY,
          String(Math.trunc(payAmount)),
        );
      } catch {
        /* remembering the amount is a convenience, never a precondition */
      }
      setConfirmOpen(false);
      if (payLink) window.open(payLink, '_blank');
    },
    onError: apiErrorHandler,
  });

  const completeTopupMutation = useMutation<void, Error, string>({
    mutationFn: async (tradeNo) => {
      unwrap(
        await API.post('/api/user/topup/complete', {
          trade_no: tradeNo,
        }),
      );
    },
    onSuccess: () => {
      notify.success(t(`${topupNs}.history.completeSuccess`));
      void Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.user }),
        queryClient.invalidateQueries({
          queryKey: queryKeys.console.detail('wallet', 'history'),
        }),
        queryClient.invalidateQueries({
          queryKey: queryKeys.console.detail('wallet', 'ledger'),
        }),
      ]);
    },
    onError: apiErrorHandler,
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
  const revealBalancePending = useDelayedReveal(balancePending);
  const revealLedgerPending = useDelayedReveal(walletLedgerQuery.isPending);

  const recentSpendUSD = recentUsageQuery.data?.totals
    ? microUSDToUSD(
        asMicroUSD(toNumber(recentUsageQuery.data.totals.amount_microusd)),
      )
    : null;
  const dailyAverage = recentSpendUSD !== null ? recentSpendUSD / 7 : null;
  const runwayDays =
    walletBalance !== null && dailyAverage !== null && dailyAverage > 0
      ? Math.max(0, Math.round(walletBalance / dailyAverage))
      : null;

  const alertThresholdUSD = parseAlertThresholdUSD(
    (user as { setting?: unknown } | null)?.setting,
  );

  const ledgerRows = useMemo(
    () =>
      buildDailyWalletLedger(
        walletLedgerQuery.data?.items || [],
        getMicroUSDPerUSD(),
      ),
    [walletLedgerQuery.data],
  );

  const statement = useMemo(
    () =>
      walletLedgerQuery.isSuccess
        ? reconcileWalletStatement({
            openingBalanceMicrousd:
              walletLedgerQuery.data?.opening_balance_microusd,
            ledgerRows,
            period,
            liveWalletBalanceUSD: walletBalance,
            microusdPerUsd: getMicroUSDPerUSD(),
          })
        : null,
    [
      ledgerRows,
      period,
      walletBalance,
      walletLedgerQuery.data?.opening_balance_microusd,
      walletLedgerQuery.isSuccess,
    ],
  );

  const initialAmount = readLastAmount(info.minTopUp) ?? TOPUP_DEFAULT;
  const payMethod = info.payMethods.find(
    (method) => method.type === selectedMethod,
  );
  const canRecharge = info.enableStripeTopUp && info.payMethods.length > 0;
  const firstTopup = useTopUpOffer(canRecharge);

  const historyColumns = useMemo<ColumnDef<TopupOrderRecord, unknown>[]>(() => {
    const columns: ColumnDef<TopupOrderRecord, unknown>[] = [
      {
        header: t(`${topupNs}.history.orderNo`),
        accessorKey: 'trade_no',
        cell: ({ getValue }) => (
          <span className='font-mono text-xs'>{String(getValue() || '-')}</span>
        ),
      },
      {
        header: t(`${topupNs}.history.paymentMethod`),
        accessorKey: 'payment_method',
        cell: ({ getValue }) => {
          const value = String(getValue() || '');
          return value === 'stripe'
            ? t(`${topupNs}.history.payment.stripe`)
            : value || '-';
        },
      },
      {
        header: t(`${topupNs}.history.quota`),
        id: 'credited_microusd',
        cell: ({ row }) =>
          `$${formatCurrency(
            resolveTopupCreditUSD(row.original, getMicroUSDPerUSD()),
          )}`,
      },
      {
        header: t(`${topupNs}.history.money`),
        id: 'paid_amount',
        cell: ({ row }) =>
          `$${formatCurrency(resolveTopupPaidUSD(row.original))}`,
      },
      {
        header: t(`${topupNs}.history.status`),
        accessorKey: 'status',
        cell: ({ getValue }) => {
          const status = String(getValue() || 'pending');
          const labels: Record<string, string> = {
            canceled: t(`${topupNs}.history.status.canceled`),
            expired: t(`${topupNs}.history.status.expired`),
            pending: t(`${topupNs}.history.status.pending`),
            success: t(`${topupNs}.history.status.success`),
          };
          return (
            <Badge variant={STATUS_VARIANTS[status] || 'neutral'}>
              {labels[status] || status}
            </Badge>
          );
        },
      },
      {
        header: t(`${topupNs}.history.effective`),
        id: 'effective_time',
        cell: ({ row }) =>
          formatTimestamp(resolveTopupEffectiveTimestamp(row.original)),
      },
    ];
    if (rootUser) {
      columns.push({
        header: t(`${topupNs}.history.action`),
        id: 'action',
        cell: ({ row }) =>
          row.original.status === 'pending' ? (
            <Button
              loading={completeTopupMutation.isPending}
              onClick={() => {
                if (row.original.trade_no)
                  setCompleteTradeNo(row.original.trade_no);
              }}
              size='sm'
              variant='ghost'
            >
              {t(`${topupNs}.history.complete`)}
            </Button>
          ) : null,
      });
    }
    return columns;
  }, [completeTopupMutation.isPending, rootUser, t]);

  const exportLedger = () => {
    const header = [
      t(`${ns}.ledger.date`, { defaultValue: '日期' }),
      t(`${ns}.ledger.credit`, { defaultValue: '入账' }),
      t(`${ns}.chart.spend`, { defaultValue: '消耗' }),
      t(`${ns}.ledger.closingBalance`, { defaultValue: '结余' }),
    ];
    const body = ledgerRows.map((row) =>
      [
        row.date,
        row.credit ? row.credit.toFixed(2) : '',
        row.spend ? (-row.spend).toFixed(2) : '',
        row.balance.toFixed(2),
      ].join(','),
    );
    const blob = new Blob([[header.join(','), ...body].join('\n')], {
      type: 'text/csv;charset=utf-8;',
    });
    const link = document.createElement('a');
    link.href = URL.createObjectURL(blob);
    link.download = `lazu-wallet-${rangeFrom.format('YYYY-MM')}.csv`;
    link.click();
    URL.revokeObjectURL(link.href);
  };

  const startPayment = (chosen: number) => {
    setAmount(chosen);
    if (!info.enableStripeTopUp) {
      showError(t(`${topupNs}.error.stripeDisabled`));
      return;
    }
    if (!chosen || chosen < info.minTopUp) {
      showError(t(`${topupNs}.error.minAmount`, { amount: info.minTopUp }));
      return;
    }
    setConfirmOpen(true);
  };

  const billingActions =
    activeTab === 'billing' ? (
      <>
        <Tabs
          value={period}
          onValueChange={(value) => setPeriod(value as 'current' | 'previous')}
        >
          <TabsList>
            <TabsTrigger value='current'>
              {t(`${ns}.period.current`, { defaultValue: '本期' })}
            </TabsTrigger>
            <TabsTrigger value='previous'>
              {t(`${ns}.period.previous`, { defaultValue: '上期' })}
            </TabsTrigger>
          </TabsList>
        </Tabs>
        <Button
          disabled={!ledgerRows.length}
          onClick={exportLedger}
          size='sm'
          variant='secondary'
        >
          <Download aria-hidden='true' size={14} />
          {t(`${ns}.action.export`, { defaultValue: '导出 CSV' })}
        </Button>
      </>
    ) : null;

  const walletTabs = (
    <Tabs value={activeTab} onValueChange={handleTabChange}>
      <TabsList
        aria-label={t(`${ns}.tabs.label`, { defaultValue: '钱包视图' })}
      >
        <TabsTrigger value='topup'>
          {t(`${ns}.recharge.title`, { defaultValue: '充值' })}
        </TabsTrigger>
        <TabsTrigger value='billing'>
          {t('nav.billing', { defaultValue: '账单' })}
        </TabsTrigger>
      </TabsList>
    </Tabs>
  );

  return (
    <ConsolePage
      appearance='review'
      title={t(`${ns}.title`, { defaultValue: '钱包' })}
      actions={billingActions}
      tabs={walletTabs}
      className='wallet-page'
    >
      {recordId ? (
        <TopupRecord
          className='wal-record'
          documentError={Boolean(searchParams.get('document_error'))}
          onClose={closeRecord}
          topupId={recordId}
        />
      ) : null}

      {activeTab === 'topup' ? (
        <div className='wal-topup-stage'>
          <section className='wal-card wal-balance-summary'>
            <div className='wal-balance-head'>
              <div>
                <span className='wal-balance-label'>
                  {t('wallet.available', { defaultValue: '可用余额' })}
                </span>
                <strong className='wal-balance-value'>
                  {balancePending ? (
                    <PendingValue
                      chars={6}
                      className={
                        !revealBalancePending ? 'invisible' : undefined
                      }
                    />
                  ) : walletBalance === null ? (
                    '—'
                  ) : (
                    `$${formatCurrency(walletBalance)}`
                  )}
                </strong>
                {runwayDays !== null ? (
                  <p
                    className='wal-runway-note'
                    data-warn={runwayDays <= 7 ? '' : undefined}
                  >
                    {t(`${ns}.runway7d`, {
                      days: runwayDays,
                      defaultValue: '按近 7 日消耗，预计可用约 {{days}} 天',
                    })}
                  </p>
                ) : null}
              </div>
              <Link className='wal-alert-link' to='/console/personal'>
                {alertThresholdUSD !== null && alertThresholdUSD >= 0.01
                  ? t(`${ns}.alertThresholdLink`, {
                      amount: `$${formatCurrency(alertThresholdUSD)}`,
                      defaultValue: '余额提醒 <{{amount}} →',
                    })
                  : t(`${ns}.alertSettingLink`, {
                      defaultValue: '余额提醒设置 →',
                    })}
              </Link>
            </div>

            {canRecharge ? (
              <TopupBar
                balanceUSD={walletBalance}
                balancePending={balancePending}
                dailyAvgUSD={dailyAverage}
                bonusLabel={firstTopup.bonusLabel}
                bonusUSD={firstTopup.bonusUSD}
                className='wal-topup-bar wal-topup-bar--embedded'
                disabled={!info.enableStripeTopUp || !info.payMethods.length}
                initialAmount={initialAmount}
                inputId='wallet-custom-amount'
                max={info.maxTopUp}
                method={
                  info.payMethods.length > 1 ? (
                    <div className='wal-methods' role='group'>
                      {info.payMethods.map((method) => (
                        <button
                          aria-pressed={selectedMethod === method.type}
                          className='wal-method'
                          key={method.type}
                          onClick={() => setSelectedMethod(method.type)}
                          type='button'
                        >
                          {method.name || method.type}
                        </button>
                      ))}
                    </div>
                  ) : (
                    t('page.console.wallet.topupBar.methodStripe')
                  )
                }
                min={info.minTopUp}
                note={firstTopup.note}
                onPay={startPayment}
                paying={paymentMutation.isPending}
                title={t(`${ns}.topupBar.amounts`, {
                  defaultValue: '充值金额',
                })}
              />
            ) : (
              <div className='wal-topup-empty'>
                <div className='wal-topup-empty__copy'>
                  <strong>
                    {t(`${topupNs}.recharge.unavailableTitle`, {
                      defaultValue: '在线充值还没开通',
                    })}
                  </strong>
                  <span>
                    {t(`${topupNs}.recharge.unavailableBody`, {
                      defaultValue:
                        '这个站点还没有配置支付方式。联系我们，确认后会为你的账户开通，余额照常可用。',
                    })}
                  </span>
                </div>
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
                  size='sm'
                  variant='secondary'
                >
                  {t(`${topupNs}.recharge.contact`, {
                    defaultValue: '联系我们开通',
                  })}
                </Button>
              </div>
            )}
          </section>

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
        <div className='wal-billing-stage'>
          <section
            aria-label={t(`${ns}.statement.aria`, { defaultValue: '对账摘要' })}
            className='wal-card wal-statement-strip'
          >
            {[
              {
                key: 'opening',
                label: t('wallet.opening', { defaultValue: '期初余额' }),
                value: statement?.opening ?? null,
                operator: '',
                tone: undefined,
              },
              {
                key: 'credits',
                label: t('wallet.credits', { defaultValue: '入账' }),
                value: statement?.credits ?? null,
                operator: '+',
                tone: statement && statement.credits > 0 ? 'credit' : undefined,
              },
              {
                key: 'spend',
                label: t(`${ns}.chart.spend`, { defaultValue: '消耗' }),
                value: statement?.spend ?? null,
                operator: '−',
                tone: undefined,
              },
              {
                key: 'closing',
                label:
                  period === 'current'
                    ? t(`${ns}.statement.currentClosing`, {
                        defaultValue: '当前结余',
                      })
                    : t('wallet.closing', { defaultValue: '期末余额' }),
                value: statement?.closing ?? null,
                operator: '=',
                tone: period === 'current' ? 'closing' : undefined,
              },
            ].map((item) => (
              <div className='wal-statement-group' key={item.key}>
                {item.operator ? (
                  <span aria-hidden='true' className='wal-statement-operator'>
                    {item.operator}
                  </span>
                ) : null}
                <div className='wal-statement-cell'>
                  <span className='wal-statement-cell__label'>
                    {item.label}
                  </span>
                  <strong
                    className='wal-statement-cell__value'
                    data-tone={item.tone}
                  >
                    {walletLedgerQuery.isPending ? (
                      <PendingValue
                        chars={6}
                        className={
                          !revealLedgerPending ? 'invisible' : undefined
                        }
                      />
                    ) : item.value === null ? (
                      '—'
                    ) : (
                      `$${formatCurrency(item.value)}`
                    )}
                  </strong>
                </div>
              </div>
            ))}
          </section>

          <section className='wal-card wal-billing-card'>
            <div className='wal-billing-card__head'>
              <Tabs value={billingView} onValueChange={handleBillingViewChange}>
                <TabsList>
                  <TabsTrigger value='daily'>
                    {t(`${ns}.billing.daily`, { defaultValue: '每日汇总' })}
                  </TabsTrigger>
                  <TabsTrigger value='orders'>
                    {historyQuery.data && historyQuery.data.total > 0
                      ? t(`${ns}.billing.ordersCount`, {
                          count: historyQuery.data.total,
                          defaultValue: '充值订单 ({{count}})',
                        })
                      : t(`${ns}.billing.orders`, { defaultValue: '充值订单' })}
                  </TabsTrigger>
                </TabsList>
              </Tabs>

              {billingView === 'daily' ? (
                <span className='wal-billing-card__hint'>
                  {t(`${ns}.ledger.clickDateHint`, {
                    defaultValue: '点击日期查当日请求',
                  })}
                </span>
              ) : (
                <div className='wal-orders-tools'>
                  <div className='wal-orders-search'>
                    <Search
                      aria-hidden='true'
                      className='wal-orders-search__icon'
                      size={14}
                    />
                    <Input
                      aria-label={t(`${topupNs}.history.search`)}
                      className='wal-orders-search__input'
                      id='wallet-history-search'
                      onChange={(event: ChangeEvent<HTMLInputElement>) => {
                        setHistoryKeyword(event.target.value);
                        setHistoryPage(1);
                      }}
                      placeholder={t(`${topupNs}.history.searchPlaceholder`)}
                      value={historyKeyword}
                    />
                  </div>
                  <PaymentAdminTools />
                </div>
              )}
            </div>

            {billingView === 'daily' ? (
              walletLedgerQuery.isError ? (
                <div className='wal-billing-card__pad'>
                  <InlineNotice
                    tone='danger'
                    title={t(`${ns}.ledger.loadError`)}
                    actions={
                      <Button
                        size='sm'
                        variant='ghost'
                        onClick={() => void walletLedgerQuery.refetch()}
                      >
                        {t('common.retry', { defaultValue: '重试' })}
                      </Button>
                    }
                  />
                </div>
              ) : walletLedgerQuery.isPending ? (
                <div className='wal-billing-card__pad'>
                  <PendingBlock
                    height={168}
                    className={!revealLedgerPending ? 'invisible' : undefined}
                  />
                </div>
              ) : ledgerRows.length ? (
                <div className='lazu-table-wrap'>
                  <table className='wal-ledger'>
                    <thead>
                      <tr>
                        <th>
                          {t(`${ns}.ledger.date`, { defaultValue: '日期' })}
                        </th>
                        <th className='wal-ledger__num'>
                          {t(`${ns}.ledger.credit`, { defaultValue: '入账' })}
                        </th>
                        <th className='wal-ledger__num'>
                          {t(`${ns}.chart.spend`, { defaultValue: '消耗' })}
                        </th>
                        <th className='wal-ledger__num'>
                          {t(`${ns}.ledger.closingBalance`, {
                            defaultValue: '结余',
                          })}
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {ledgerRows.map((row) => (
                        <tr key={row.id}>
                          <td>
                            <Link
                              aria-label={t(`${ns}.ledger.inspect`, {
                                date: row.date,
                                defaultValue: `Inspect requests from ${row.date}`,
                              })}
                              className='wal-ledger__date-link'
                              to={`/console/usage?tab=logs&start_timestamp=${dayjs(row.date).startOf('day').unix()}&end_timestamp=${dayjs(row.date).endOf('day').unix()}`}
                            >
                              {dayjs(row.date).format('MM-DD')}
                            </Link>
                          </td>
                          <td className='wal-ledger__num'>
                            {row.credit ? (
                              <span className='wal-ledger__credit'>
                                {formatSigned(row.credit)}
                              </span>
                            ) : (
                              <span className='wal-ledger__idle'>—</span>
                            )}
                          </td>
                          <td className='wal-ledger__num'>
                            {row.spend ? (
                              formatSigned(-row.spend)
                            ) : (
                              <span className='wal-ledger__idle'>—</span>
                            )}
                          </td>
                          <td className='wal-ledger__num wal-ledger__closing'>
                            ${formatCurrency(row.balance)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <p className='wal-ledger__empty'>
                  {t(`${billingNs}.ledger.emptyDescription`)}
                </p>
              )
            ) : (
              <div className='wal-orders-body'>
                {historyKeywordTooShort ? (
                  <FieldMessage>
                    {t(`${topupNs}.history.searchMinimum`)}
                  </FieldMessage>
                ) : null}
                {historyQuery.error ? (
                  <FieldMessage error>
                    {errorMessage(
                      historyQuery.error,
                      t(`${topupNs}.error.loadHistory`),
                    )}
                  </FieldMessage>
                ) : null}
                <DataTableShell
                  columns={historyColumns}
                  data={historyQuery.data?.items || []}
                  empty={<EmptyState title={t(`${topupNs}.history.empty`)} />}
                  loading={historyQuery.isPending}
                  rowKey={(row) => row.id || row.trade_no || ''}
                />
                {(historyQuery.data?.total || 0) > historyPageSize ? (
                  <Pagination
                    currentPage={historyPage}
                    onPageChange={setHistoryPage}
                    onPageSizeChange={(size) => {
                      setHistoryPageSize(size);
                      setHistoryPage(1);
                    }}
                    pageSize={historyPageSize}
                    showSizeChanger
                    total={historyQuery.data?.total || 0}
                  />
                ) : null}
              </div>
            )}
          </section>
        </div>
      )}

      <Modal
        open={confirmOpen}
        busy={paymentMutation.isPending}
        onOpenChange={setConfirmOpen}
        title={t(`${topupNs}.confirm.title`)}
        description={t(`${topupNs}.confirm.description`, {
          method: payMethod?.name || selectedMethod,
        })}
        body={
          <div className='grid gap-3'>
            <div className='wal-summary'>
              <span>{t(`${topupNs}.confirm.amount`)}</span>
              <b>${formatCurrency(amount || 0)}</b>
            </div>
            <div className='wal-summary' style={{ borderBottom: 0 }}>
              <span>{t(`${topupNs}.recharge.method`)}</span>
              <b>{payMethod?.name || selectedMethod}</b>
            </div>
          </div>
        }
        footer={
          <>
            <Button
              disabled={paymentMutation.isPending}
              onClick={() => setConfirmOpen(false)}
              variant='ghost'
            >
              {t(`${topupNs}.confirm.cancel`)}
            </Button>
            <Button
              loading={paymentMutation.isPending}
              onClick={() =>
                paymentMutation.mutate({ amount, method: selectedMethod })
              }
              variant='primary'
            >
              {t(`${topupNs}.confirm.pay`)}
            </Button>
          </>
        }
      />

      <Modal
        open={completeTradeNo !== null}
        busy={completeTopupMutation.isPending}
        onOpenChange={(open) => {
          if (!open) setCompleteTradeNo(null);
        }}
        title={t(`${topupNs}.history.complete`)}
        body={<p>{t(`${topupNs}.history.completeConfirm`)}</p>}
        footer={
          <>
            <Button
              disabled={completeTopupMutation.isPending}
              variant='secondary'
              onClick={() => setCompleteTradeNo(null)}
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              loading={completeTopupMutation.isPending}
              onClick={() => {
                if (completeTradeNo)
                  completeTopupMutation.mutate(completeTradeNo, {
                    onSuccess: () => setCompleteTradeNo(null),
                  });
              }}
            >
              {t(`${topupNs}.history.complete`)}
            </Button>
          </>
        }
      />
    </ConsolePage>
  );
};

export default WalletPage;

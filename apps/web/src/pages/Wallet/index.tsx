import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { CreditCard, Download, History, Search } from 'lucide-react';
import { useContext, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useSearchParams } from 'react-router-dom';
import {
  resolveTopupCreditUSD,
  resolveTopupEffectiveTimestamp,
  resolveTopupPaidUSD,
} from './walletAmounts';
import { buildDailyWalletLedger, type WalletLedgerDay } from './walletLedger';
import VChart from '../../components/common/charts/VChart';
import {
  Badge,
  Button,
  DataTableShell,
  EmptyState,
  Field,
  FieldMessage,
  Input,
  InlineNotice,
  Label,
  Modal,
  Pagination,
  Tabs,
  TabsList,
  TabsTrigger,
  TOPUP_DEFAULT,
  TOPUP_MAX_DEFAULT,
  TOPUP_MIN_DEFAULT,
  TopupBar,
  TopupRecord,
} from '../../components/ui';
import ConsolePage from '../../components/ui/console-page';
import { PaymentAdminTools } from '../../components/ui/payment-admin-tools';
import { CHART_CONFIG } from '../../constants/dashboard.constants';
import { StatusContext } from '../../context/Status';
import { useSession } from '../../context/User';
import { useTopUpOffer, webExtensions } from '../../extensions';
import {
  API,
  asMicroUSD,
  getMicroUSDPerUSD,
  isRoot,
  microUSDToUSD,
  showError,
  showSuccess,
  timestamp2string,
} from '../../helpers';
import { getSupportEmail } from '../../helpers/brandUrls';
import { useSelfQuery } from '../../hooks/common/useSelfQuery';
import { apiErrorHandler, errorMessage, unwrap } from '../../lib/api-error';
import { readBrandToken, useBodyThemeMode } from '../../lib/brand-token';
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

type DashboardTimeRow = {
  amount_microusd?: number;
  bucket?: number;
};

type DashboardUsageData = {
  time_series?: DashboardTimeRow[];
  totals?: {
    amount_microusd?: number;
    outcome_count?: number;
    request_count?: number;
  };
};

type TopupRecord = Record<string, unknown> & {
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
  `${value < 0 ? '−' : '+'}${formatCurrency(Math.abs(value))}`;

/** The amount the reader last took to checkout. It is deliberately not "the
 *  last top-up": a created checkout may be abandoned, and this page never
 *  learns the outcome, so it remembers the choice rather than claiming a
 *  completed payment. */
const LAST_AMOUNT_STORAGE_KEY = 'lazu:wallet:last-selected-amount';

/** Reading a stored amount is a convenience, never a requirement: private
 *  windows and cleared storage must land on the same page. */
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

const buildDailySpend = (records: DashboardTimeRow[]) => {
  const buckets = new Map<string, number>();
  for (const record of records) {
    const timestamp = toNumber(record.bucket);
    if (!timestamp) continue;
    const key = dayjs(
      timestamp > 10_000_000_000 ? timestamp : timestamp * 1000,
    ).format('YYYY-MM-DD');
    buckets.set(
      key,
      (buckets.get(key) || 0) +
        microUSDToUSD(asMicroUSD(toNumber(record.amount_microusd))),
    );
  }
  return buckets;
};

const WalletPage = () => {
  const { t } = useTranslation();
  const [searchParams, setSearchParams] = useSearchParams();
  /* The Stripe return and both payment e-mails land here with the order id;
     the record card is pinned above the ledger until the reader closes it. */
  const recordId = searchParams.get('top_up_id')?.trim() || '';
  const closeRecord = () => {
    const next = new URLSearchParams(searchParams);
    for (const key of ['top_up_id', 'project_id', 'document', 'document_error'])
      next.delete(key);
    setSearchParams(next, { replace: true });
  };
  const queryClient = useQueryClient();
  const userSession = useSession();
  const [statusState] = useContext(StatusContext);
  const [period, setPeriod] = useState<'current' | 'previous'>('current');
  const [selectedMethod, setSelectedMethod] = useState('stripe');
  const [confirmOpen, setConfirmOpen] = useState(false);
  /* The amount the reader pressed pay with; the confirmation shows it. */
  const [amount, setAmount] = useState(0);
  const [completeTradeNo, setCompleteTradeNo] = useState<string | null>(null);
  const ownerParam = isRoot() ? searchParams.get('owner')?.trim() || '' : '';
  const [historyOpen, setHistoryOpen] = useState(Boolean(ownerParam));
  const [historyPage, setHistoryPage] = useState(1);
  const [historyPageSize, setHistoryPageSize] = useState(10);
  const [historyKeyword, setHistoryKeyword] = useState('');
  const [historyOwner, setHistoryOwner] = useState(ownerParam);

  // React keeps this page mounted when only the query string changes. Keep
  // the modal's exact-owner scope aligned with the URL instead of retaining a
  // stale account after navigating to another owner (or back to global).
  useEffect(() => {
    setHistoryOwner(ownerParam);
    setHistoryKeyword('');
    setHistoryPage(1);
    setHistoryOpen(Boolean(ownerParam));
  }, [ownerParam]);

  const anchor = period === 'current' ? dayjs() : dayjs().subtract(1, 'month');
  const rangeFrom = anchor.startOf('month');
  const rangeTo =
    period === 'current' ? dayjs().endOf('day') : anchor.endOf('month');
  const startTimestamp = rangeFrom.unix();
  const endTimestamp = rangeTo.unix();

  const userQuery = useSelfQuery(Boolean(userSession.user?.id));
  useEffect(() => {
    const data = userQuery.data;
    if (data) {
      userSession.signIn(data);
    }
  }, [userQuery.data, userSession.signIn]);

  const usageQuery = useQuery({
    queryKey: [
      ...queryKeys.dashboard.summary(),
      'wallet',
      startTimestamp,
      endTimestamp,
    ],
    queryFn: async () => {
      const res = await API.get('/api/data/dashboard/self', {
        params: {
          start_timestamp: startTimestamp,
          end_timestamp: endTimestamp,
        },
        skipErrorHandler: true,
      });
      return (unwrap<DashboardUsageData | null>(res) ||
        {}) as DashboardUsageData;
    },
  });

  const topupInfoQuery = useQuery({
    queryKey: [...queryKeys.console.detail('wallet', 'info')],
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
    queryFn: async () => {
      const res = await API.get('/api/user/wallet/ledger', {
        params: {
          start_timestamp: startTimestamp,
          // The wallet endpoint uses a half-open range so sub-second ledger
          // events at the end of the selected day cannot be dropped.
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
    enabled: historyOpen,
    queryFn: async () => {
      const base = isRoot() ? '/api/user/topup' : '/api/user/topup/self';
      // The failure is shown under the search field, so the global toast
      // would say it twice.
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
        items: (Array.isArray(data?.items) ? data.items : []) as TopupRecord[],
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
        // Only once the checkout actually exists, and only as the amount the
        // reader chose — a rejected request must not become their default.
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
      showSuccess(t(`${topupNs}.history.completeSuccess`));
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
  const lifetimeSpend =
    user?.used_quota === undefined || user?.used_quota === null
      ? null
      : microUSDToUSD(asMicroUSD(user.used_quota));
  const totals = usageQuery.data?.totals;
  const periodSpend = totals
    ? microUSDToUSD(asMicroUSD(toNumber(totals.amount_microusd)))
    : null;
  const periodRequests = totals
    ? toNumber(totals.outcome_count ?? totals.request_count)
    : null;

  const spendByDay = useMemo(
    () => buildDailySpend(usageQuery.data?.time_series || []),
    [usageQuery.data?.time_series],
  );
  const ledgerRows = useMemo(
    () =>
      buildDailyWalletLedger(
        walletLedgerQuery.data?.items || [],
        getMicroUSDPerUSD(),
      ),
    [walletLedgerQuery.data],
  );

  const chartSeries = useMemo(() => {
    const days = Math.max(1, rangeTo.endOf('day').diff(rangeFrom, 'day') + 1);
    return Array.from({ length: days }, (_, index) => {
      const day = rangeFrom.add(index, 'day');
      return {
        amount: spendByDay.get(day.format('YYYY-MM-DD')) || 0,
        date: day.format('MM-DD'),
      };
    });
  }, [rangeFrom, rangeTo, spendByDay]);

  const elapsedDays = Math.max(
    1,
    (period === 'current' ? dayjs() : rangeTo).diff(rangeFrom, 'day') + 1,
  );
  const dailyAverage = (periodSpend ?? 0) / elapsedDays;
  const projected = dailyAverage * rangeFrom.daysInMonth();

  const bodyTheme = useBodyThemeMode();
  const chartSpec = useMemo(() => {
    /* Charts draw in the brand's teal chart color — orange is banned in data
       viz (BRAND.md). Read per theme so dark gets its lifted teal. */
    const accent = readBrandToken('--lazu-chart-1', '#1f6f6b');
    const grid = readBrandToken('--lazu-data-grid', '#e4e4e7');
    return {
      type: 'line',
      data: [{ id: 'wallet-usage', values: chartSeries }],
      xField: 'date',
      yField: 'amount',
      smooth: true,
      area: { visible: true, style: { fill: accent, fillOpacity: 0.16 } },
      line: { style: { lineWidth: 2, stroke: accent } },
      point: { visible: false },
      axes: [
        {
          orient: 'left',
          grid: { style: { stroke: grid } },
          label: {
            formatMethod: (value: number | string) => `$${value}`,
          },
        },
      ],
      tooltip: {
        mark: {
          content: [
            {
              key: t(`${billingNs}.chart.spend`),
              value: (datum: { amount: number }) =>
                `$${datum.amount.toFixed(2)}`,
            },
          ],
        },
      },
      color: [accent],
    };
  }, [bodyTheme, chartSeries, t]);

  /* Opens on the amount the reader last took to checkout when there is one,
     otherwise on the bar's default preset. */
  const initialAmount = readLastAmount(info.minTopUp) ?? TOPUP_DEFAULT;
  const payMethod = info.payMethods.find(
    (method) => method.type === selectedMethod,
  );
  /* No configured payment method means this deployment does not sell credit —
     the page is then a pure ledger and the recharge rail would be dead chrome. */
  const canRecharge = info.enableStripeTopUp && info.payMethods.length > 0;
  const firstTopup = useTopUpOffer(canRecharge);

  const historyColumns = useMemo<ColumnDef<TopupRecord, unknown>[]>(() => {
    const columns: ColumnDef<TopupRecord, unknown>[] = [
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
          timestamp2string(resolveTopupEffectiveTimestamp(row.original)),
      },
    ];
    if (isRoot()) {
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
  }, [completeTopupMutation, t]);

  const exportLedger = () => {
    const header = [
      t(`${ns}.ledger.date`, { defaultValue: '日期' }),
      t(`${ns}.ledger.spend`, { defaultValue: '消耗' }),
      t(`${ns}.ledger.credit`, { defaultValue: '充值' }),
      t(`${ns}.ledger.balance`, { defaultValue: '余额' }),
    ];
    const body = ledgerRows.map((row) =>
      [
        row.date,
        row.spend ? (-row.spend).toFixed(2) : '',
        row.credit ? row.credit.toFixed(2) : '',
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

  const actions = (
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
        onClick={() => setHistoryOpen(true)}
        size='sm'
        variant='secondary'
      >
        <History aria-hidden='true' size={14} />
        {t(`${topupNs}.action.history`)}
      </Button>
      <Button
        disabled={!ledgerRows.length}
        onClick={exportLedger}
        size='sm'
        variant='secondary'
      >
        <Download aria-hidden='true' size={14} />
        {t(`${ns}.action.export`, { defaultValue: '导出 CSV' })}
      </Button>
      <PaymentAdminTools />
    </>
  );

  return (
    <ConsolePage
      appearance='review'
      title={t(`${ns}.title`, { defaultValue: '钱包' })}
      actions={actions}
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
      <div className='wal-layout'>
        <div className='wal-main'>
          <section className='wal-card wal-balance-summary'>
            <span>{t('wallet.available', { defaultValue: '可用余额' })}</span>
            <strong className='wal-balance-value'>
              {walletBalance === null
                ? '—'
                : `$${formatCurrency(walletBalance)}`}
            </strong>
            <p>
              {rangeFrom.format('YYYY.MM.DD')} — {rangeTo.format('MM.DD')} · USD
            </p>
            {/* 期初 + 入账 − 净支出 = 期末. The four figures were already here
                as evenly spaced numbers; the operators are what make them
                reconcile instead of read as an unrelated row of stats. */}
            <div className='wal-balance-equation'>
              {[
                [
                  t('wallet.opening', { defaultValue: '期初余额' }),
                  walletLedgerQuery.data?.opening_balance_microusd == null
                    ? null
                    : walletLedgerQuery.data.opening_balance_microusd /
                      getMicroUSDPerUSD(),
                  '',
                ],
                [
                  t('wallet.credits', { defaultValue: '入账' }),
                  walletLedgerQuery.isSuccess
                    ? ledgerRows.reduce((sum, row) => sum + row.credit, 0)
                    : null,
                  '+',
                ],
                [
                  t('wallet.spend', { defaultValue: '净支出' }),
                  walletLedgerQuery.isSuccess
                    ? ledgerRows.reduce((sum, row) => sum + row.spend, 0)
                    : null,
                  '−',
                ],
                [
                  t('wallet.closing', { defaultValue: '期末余额' }),
                  walletLedgerQuery.isSuccess
                    ? (ledgerRows[0]?.balance ??
                      (walletLedgerQuery.data?.opening_balance_microusd == null
                        ? null
                        : walletLedgerQuery.data.opening_balance_microusd /
                          getMicroUSDPerUSD()))
                    : null,
                  '=',
                ],
              ].map(([label, value, operator]) => (
                <div key={String(label)}>
                  {operator ? (
                    <span aria-hidden='true' className='wal-balance-operator'>
                      {operator}
                    </span>
                  ) : null}
                  <span className='wal-balance-cell'>
                    <small>{label}</small>
                    <strong>
                      {value == null
                        ? '—'
                        : `$${formatCurrency(Number(value))}`}
                    </strong>
                  </span>
                </div>
              ))}
            </div>
          </section>

          {usageQuery.isError ? (
            <InlineNotice
              tone='danger'
              title={t(`${billingNs}.error.loadUsage`)}
              actions={
                <Button
                  size='sm'
                  variant='ghost'
                  onClick={() => void usageQuery.refetch()}
                >
                  {t('common.retry', { defaultValue: '重试' })}
                </Button>
              }
            />
          ) : null}
          {/* Was a <details> nobody opened. Daily spend is the reason the
              balance moves, so it sits on the page next to the balance. */}
          {canRecharge ? (
            <TopupBar
              balanceUSD={walletBalance}
              balancePending={
                userQuery.isPending ||
                (userQuery.isFetching && walletBalance === null)
              }
              dailyAvgUSD={periodSpend === null ? null : dailyAverage}
              bonusLabel={firstTopup.bonusLabel}
              bonusUSD={firstTopup.bonusUSD}
              className='wal-topup-bar'
              disabled={!info.enableStripeTopUp || !info.payMethods.length}
              initialAmount={initialAmount}
              inputId='wallet-custom-amount'
              max={info.maxTopUp}
              method={
                info.payMethods.length > 1 ? (
                  <div className='wal-methods'>
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
              title={t(`${ns}.recharge.title`, { defaultValue: '充值' })}
            />
          ) : (
            /* No payment method: the top-up's own slot says why and offers
               the one way forward, in the place the top-up will occupy. */
            <section className='wal-topup-bar wal-topup-empty'>
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
          <section className='wal-card wal-chart-card'>
            <div className='wal-chart-card__head'>
              <b>{t(`${ns}.chart.title`, { defaultValue: '本期消耗' })}</b>
              <div className='wal-chart-card__figures'>
                <span>
                  {t(`${ns}.chart.spend`, { defaultValue: '消耗' })}{' '}
                  <b>
                    {periodSpend === null
                      ? '—'
                      : `$${formatCurrency(periodSpend)}`}
                  </b>
                </span>
                <span>
                  {t(`${ns}.chart.balance`, { defaultValue: '余额' })}{' '}
                  <b>
                    {walletBalance === null
                      ? '—'
                      : `$${formatCurrency(walletBalance)}`}
                  </b>
                </span>
                <span>
                  {t(`${ns}.chart.requests`, { defaultValue: '请求' })}{' '}
                  <b>
                    {periodRequests === null
                      ? '—'
                      : periodRequests.toLocaleString()}
                  </b>
                </span>
              </div>
            </div>
            {usageQuery.isError ? (
              <div className='wal-chart-card__empty'>
                {t(`${billingNs}.error.loadUsage`)}
              </div>
            ) : usageQuery.isLoading ? (
              <div className='wal-chart-card__empty'>
                {t('page.system.loading.title')}
              </div>
            ) : periodSpend !== null && periodSpend > 0 ? (
              <>
                <div className='wal-chart-card__canvas'>
                  <VChart spec={chartSpec} option={CHART_CONFIG} />
                </div>
                {/* The axis already dates the range; the foot keeps only
                    the pace it implies. */}
                <div className='wal-chart-card__foot'>
                  <span>
                    {t(`${ns}.chart.pace`, {
                      average: formatCurrency(dailyAverage),
                      projected: formatCurrency(projected),
                      defaultValue:
                        '日均 ${{average}} · 预计月末 ${{projected}}',
                    })}
                  </span>
                </div>
              </>
            ) : (
              <div className='wal-chart-card__empty'>
                {t(`${billingNs}.empty.usage`)}
              </div>
            )}
          </section>

          <section className='wal-card wal-ledger-card'>
            <h2>{t('wallet.balanceChanges', { defaultValue: '余额变动' })}</h2>
            {walletLedgerQuery.isError ? (
              <div className='wal-ledger__empty'>
                {t(`${ns}.ledger.loadError`)}
              </div>
            ) : walletLedgerQuery.isLoading ? (
              <div className='wal-ledger__empty'>
                {t('page.system.loading.title')}
              </div>
            ) : ledgerRows.length ? (
              <table className='wal-ledger'>
                <thead>
                  <tr>
                    <th>{t(`${ns}.ledger.date`, { defaultValue: '日期' })}</th>
                    <th className='wal-ledger__num'>
                      {t(`${ns}.ledger.spend`, { defaultValue: '消耗' })}
                    </th>
                    <th className='wal-ledger__num'>
                      {t(`${ns}.ledger.credit`, { defaultValue: '充值' })}
                    </th>
                    <th className='wal-ledger__num'>
                      {t(`${ns}.ledger.balance`, { defaultValue: '余额' })}
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
                        {row.spend ? (
                          formatSigned(-row.spend)
                        ) : (
                          <span className='wal-ledger__idle'>—</span>
                        )}
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
                        {formatCurrency(row.balance)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <div className='wal-ledger__empty'>
                {t(`${billingNs}.ledger.emptyDescription`)}
              </div>
            )}
          </section>
        </div>
      </div>

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
      <Modal
        open={historyOpen}
        onOpenChange={setHistoryOpen}
        title={t(`${topupNs}.history.title`)}
        description={t(`${topupNs}.history.description`)}
        body={
          <div className='grid max-h-[70vh] gap-4 overflow-auto'>
            <Field>
              <Label htmlFor='wallet-history-search'>
                {t(`${topupNs}.history.search`)}
              </Label>
              <div className='relative'>
                <Search
                  aria-hidden='true'
                  className='pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--ui-text-tertiary)]'
                  size={15}
                />
                <Input
                  className='pl-9'
                  id='wallet-history-search'
                  onChange={(event: ChangeEvent<HTMLInputElement>) => {
                    setHistoryKeyword(event.target.value);
                    setHistoryPage(1);
                  }}
                  placeholder={t(`${topupNs}.history.searchPlaceholder`)}
                  value={historyKeyword}
                />
              </div>
            </Field>
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
              loading={historyQuery.isLoading || historyQuery.isFetching}
              rowKey={(row) => row.id || row.trade_no || ''}
            />
            {/* A pager under an empty table offers to page through nothing.
                It appears once there is more than one page to reach. */}
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
        }
      />
    </ConsolePage>
  );
};

export default WalletPage;

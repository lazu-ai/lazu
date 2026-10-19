/* R7 / R8 · the wallet with the projects switch on. Same page for an
 * unshared default project (R7) and a shared one (R8): four metrics, three
 * tabs (余额变动 · 充值记录/Invoice · 月账单), the top-up card with the
 * Stripe billing line, and the fixed DUBRIO LLC footnote. Members get the
 * "我的预算" page instead (S7.10). No self-built billing form exists. */
import { useQuery } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { Download } from 'lucide-react';
import { useContext, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link, useSearchParams } from 'react-router-dom';
import {
  Button,
  Input,
  Tabs,
  TabsList,
  TabsTrigger,
} from '../../../components/ui';
import ConsolePage from '../../../components/ui/console-page';
import { PaymentAdminTools } from '../../../components/ui/payment-admin-tools';
import { TopupBar } from '../../../components/ui/topup-bar';
import { TopupRecord } from '../../../components/ui/topup-record';
import { StatusContext } from '../../../context/Status';
import { useTopUpOffer, webExtensions } from '../../../extensions';
import { getMicroUSDPerUSD } from '../../../helpers';
import { API } from '../../../helpers/api';
import { errorMessage, unwrap } from '../../../lib/api-error';
import { buildDailyWalletLedger } from '../../../pages/Wallet/walletLedger';
import { canManageBilling, canTopUp as roleCanTopUp } from '../access';
import { validateTopUpAmount } from '../budget';
import {
  Card,
  ErrLine,
  Kvs,
  Ledger,
  PjBadge,
  SectionHead,
  TableFoot,
  formatDay,
  formatMonthDay,
} from '../components/atoms';
import { ShareBalanceCard } from '../components/ShareBalanceCard';
import { formatUSD, formatUSDShort, toMicro } from '../money';
import { paymentDocumentPath } from '../paymentDocument';
import { useProject } from '../ProjectContext';
import {
  useMyBudget,
  useMyProjectTokens,
  useProjectDetail,
  useProjectMembers,
  useProjectTopups,
  useProjectWallet,
} from '../queries';
import { projectsQueryRoot } from '../queryKeys';
import { errorReceipt } from '../receipt';
import { useOperationId } from '../useOperationId';
import type { ProjectTopup } from '../types';

type Tab = 'ledger' | 'topups' | 'monthly';

export default function ProjectWalletPage() {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  if (project.current?.isShared && !project.isManager) return <MyBudgetPage />;
  return <OwnerWallet />;
}

function WalletPaymentRecord() {
  const [params, setParams] = useSearchParams();
  const id = params.get('top_up_id')?.trim();
  if (!id) return null;
  return (
    <TopupRecord
      topupId={id}
      className='pj-topup-record'
      documentError={Boolean(params.get('document_error'))}
      onClose={() => {
        const next = new URLSearchParams(params);
        for (const key of [
          'top_up_id',
          'project_id',
          'document',
          'document_error',
        ])
          next.delete(key);
        setParams(next, { replace: true });
      }}
    />
  );
}

function OwnerWallet() {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const [statusState] = useContext(StatusContext);
  const wallet = useProjectWallet();
  const topups = useProjectTopups();
  const detail = useProjectDetail();
  const members = useProjectMembers({
    enabled: Boolean(project.current?.isShared),
  });
  const personalKeys = useMyProjectTokens({
    enabled: !project.current?.isShared,
  });
  const [period, setPeriod] = useState<'current' | 'previous'>('current');
  const [tab, setTab] = useState<Tab>('topups');
  const shared = Boolean(project.current?.isShared);
  const paymentOperation = useOperationId();
  const [paying, setPaying] = useState(false);
  const [portalBusy, setPortalBusy] = useState(false);
  const [searchParams] = useSearchParams();
  /* The Stripe return and both payment e-mails land here with the order id;
     the record card is pinned above the ledger until the reader closes it. */
  const recordId = searchParams.get('top_up_id')?.trim() || '';

  const anchor = period === 'current' ? dayjs() : dayjs().subtract(1, 'month');
  const rangeFrom = anchor.startOf('month');
  const rangeTo =
    period === 'current' ? dayjs().endOf('day') : anchor.endOf('month');
  const historicalWallet = useQuery({
    queryKey: [
      ...projectsQueryRoot(project.current?.id),
      'wallet-period',
      rangeFrom.unix(),
      rangeTo.unix(),
    ],
    queryFn: () =>
      project.client!.getWallet(project.current!.id, {
        start_timestamp: rangeFrom.unix(),
        end_timestamp: rangeTo.unix() + 1,
      }),
    enabled: Boolean(
      project.client && project.current && period === 'previous',
    ),
  });
  const browserTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const ledgerQuery = useQuery({
    queryKey: [
      ...projectsQueryRoot(project.current?.id),
      'wallet-ledger',
      rangeFrom.unix(),
      rangeTo.unix(),
      browserTimezone,
    ],
    queryFn: async () => {
      const res = await API.get('/api/user/wallet/ledger', {
        params: {
          start_timestamp: rangeFrom.unix(),
          end_timestamp: rangeTo.add(1, 'second').unix(),
          timezone: browserTimezone,
          timezone_offset_minutes: new Date().getTimezoneOffset(),
          project_id: project.current?.id,
        },
        skipErrorHandler: true,
      } as never);
      return (
        unwrap<{ items?: Parameters<typeof buildDailyWalletLedger>[0] }>(res) ??
        {}
      );
    },
    enabled: tab === 'ledger',
  });
  const ledgerRows = useMemo(
    () =>
      buildDailyWalletLedger(
        ledgerQuery.data?.items || [],
        getMicroUSDPerUSD(),
      ),
    [ledgerQuery.data],
  );

  const w = wallet.data;
  const minTopUp = Number(
    (statusState?.status as { stripe_min_topup?: number } | undefined)
      ?.stripe_min_topup || 1,
  );
  const maxTopUp = 10_000;
  const projectSettings = detail.data?.settings;
  const canTopUp =
    w?.capabilities?.can_topup ??
    (projectSettings
      ? roleCanTopUp(project.role, projectSettings)
      : project.isOwner);
  // A top-up offer follows the wallet owner's payments, and what extensions
  // show beside the wallet belongs to the owner, so both are owner-only.
  const firstTopup = useTopUpOffer(canTopUp && project.isOwner);
  const canPortal =
    (w?.capabilities?.can_manage_billing ?? canManageBilling(project.role)) &&
    project.isOwner;
  const monthTopups = (topups.data?.items ?? []).filter(
    (x) => x.created_at >= rangeFrom.unix() && x.created_at <= rangeTo.unix(),
  );

  const pay = async (amount: number) => {
    if (
      !project.client ||
      !project.current ||
      validateTopUpAmount(amount, minTopUp, maxTopUp) !== 'ok'
    )
      return;
    setPaying(true);
    try {
      const { pay_link, top_up_id, payment_status } =
        await project.client.startTopup(
          project.current.id,
          amount,
          'stripe',
          paymentOperation.forInput(
            JSON.stringify([project.current.id, amount, 'stripe']),
          ),
        );
      paymentOperation.completed();
      if (payment_status === 'paid') {
        window.location.assign(
          `/console/wallet?project_id=${encodeURIComponent(project.current.id)}&top_up_id=${encodeURIComponent(top_up_id)}`,
        );
        return;
      }
      window.location.assign(pay_link);
    } catch (e) {
      errorReceipt(e);
    } finally {
      setPaying(false);
    }
  };
  const openPortal = async () => {
    if (!project.client || !project.current) return;
    setPortalBusy(true);
    try {
      const { url } = await project.client.getPortalUrl(project.current.id);
      window.open(url, '_blank', 'noopener');
    } catch (e) {
      errorReceipt(e);
    } finally {
      setPortalBusy(false);
    }
  };

  const exportCsv = () => {
    const rows = topups.data?.items ?? [];
    const header = [
      'date',
      'amount_usd',
      'paid_by',
      'method',
      'invoice',
      'bill_to',
      'status',
    ];
    const body = rows.map((x) =>
      [
        formatDay(x.created_at),
        (x.paid_amount_microusd / 1e6).toFixed(2),
        x.paid_by?.username ?? '',
        x.method,
        x.invoice?.number ?? '',
        x.invoice?.bill_to?.name ?? '',
        x.payment_status,
      ].join(','),
    );
    const blob = new Blob([[header.join(','), ...body].join('\n')], {
      type: 'text/csv;charset=utf-8;',
    });
    const link = document.createElement('a');
    link.href = URL.createObjectURL(blob);
    link.download = `${project.currentName}-topups.csv`;
    link.click();
    URL.revokeObjectURL(link.href);
  };

  const cap = w?.project_cap;
  const capPct =
    cap && cap.cap_microusd
      ? Math.round((cap.spent_microusd / cap.cap_microusd) * 100)
      : null;
  const allocation = detail.data?.allocation.member_caps_microusd ?? 0;

  return (
    <ConsolePage
      appearance='review'
      className='pj-page pj-wallet-page'
      title={t('projects.wallet.title')}
      description={
        shared
          ? t('projects.wallet.ledeShared', { project: project.currentName })
          : t('projects.wallet.ledeOwn', { project: project.currentName })
      }
      actions={
        <>
          <Tabs
            onValueChange={(v) => setPeriod(v as 'current' | 'previous')}
            value={period}
          >
            <TabsList>
              <TabsTrigger value='current'>
                {t('projects.wallet.thisMonth')}
              </TabsTrigger>
              <TabsTrigger value='previous'>
                {t('projects.wallet.lastMonth')}
              </TabsTrigger>
            </TabsList>
          </Tabs>
          <PaymentAdminTools />
          <Button onClick={exportCsv} size='sm' variant='secondary'>
            <Download aria-hidden='true' size={14} />
            {t('projects.wallet.exportCsv')}
          </Button>
        </>
      }
    >
      <WalletPaymentRecord />
      <Ledger
        items={[
          {
            key: 'balance',
            label: shared
              ? t('projects.wallet.projectBalance')
              : t('projects.wallet.balance'),
            value: formatUSD(w?.balance_microusd),
            hint:
              w?.days_left !== null && w?.days_left !== undefined
                ? t('projects.wallet.balanceHint', {
                    daily: formatUSD(w.daily_avg_microusd ?? 0),
                    days: w.days_left,
                    date: formatMonthDay(
                      Math.floor(Date.now() / 1000) + w.days_left * 86_400,
                    ),
                  })
                : t('projects.wallet.balanceHintFlat', {
                    daily: formatUSD(w?.daily_avg_microusd ?? 0),
                  }),
          },
          {
            key: 'spent',
            label: t('projects.wallet.monthSpent'),
            value: formatUSD(
              period === 'previous'
                ? historicalWallet.data?.period_spend_microusd
                : w?.month_spend_microusd,
            ),
            hint: shared
              ? t('projects.wallet.monthSpentHintShared', {
                  people: members.data?.length ?? 0,
                })
              : t('projects.wallet.monthSpentHintOwn', {
                  keys: personalKeys.data?.length ?? '—',
                }),
          },
          {
            key: 'topup',
            label: t('projects.wallet.monthTopup'),
            value: formatUSD(
              period === 'previous'
                ? historicalWallet.data?.period_topup_microusd
                : w?.month_topup_microusd,
            ),
            hint: monthTopups.length
              ? t('projects.wallet.monthTopupHint', {
                  count: monthTopups.length,
                  dates: monthTopups
                    .map((x) => formatMonthDay(x.created_at))
                    .join('、'),
                  who: shared
                    ? t('projects.wallet.you')
                    : t('projects.wallet.yourself'),
                })
              : t('projects.wallet.monthTopupNone'),
          },
          shared
            ? {
                key: 'cap',
                label: t('projects.members.ledger.cap'),
                value:
                  cap?.cap_microusd != null
                    ? formatUSD(cap.cap_microusd)
                    : t('projects.budget.notSet'),
                hint:
                  cap?.cap_microusd != null
                    ? t('projects.wallet.capHint', {
                        pct: capPct ?? 0,
                        allocation: formatUSDShort(allocation),
                      })
                    : t('projects.members.ledger.capNone'),
              }
            : {
                key: 'keys',
                label: t('projects.member.myKeys'),
                value: personalKeys.data
                  ? String(personalKeys.data.length)
                  : '—',
                hint: t('projects.wallet.ownKeysHint'),
              },
        ]}
      />
      <ShareBalanceCard />
      {canTopUp ? (
        <TopupBar
          balancePending={wallet.isPending}
          balanceUSD={
            w?.balance_microusd == null
              ? null
              : w.balance_microusd / getMicroUSDPerUSD()
          }
          bonusLabel={firstTopup.bonusLabel}
          bonusUSD={firstTopup.bonusUSD}
          className='pj-topup-bar'
          dailyAvgUSD={
            w?.daily_avg_microusd
              ? w.daily_avg_microusd / getMicroUSDPerUSD()
              : null
          }
          max={maxTopUp}
          method={t('page.console.wallet.topupBar.methodStripe')}
          min={minTopUp}
          note={
            <>
              {firstTopup.note}
              {firstTopup.note && (shared || canPortal) ? ' · ' : null}
              {shared ? t('projects.wallet.topUpSubShared') : null}
              {canPortal ? (
                <>
                  {shared ? ' · ' : null}
                  <button
                    className='pj-linkbtn'
                    disabled={portalBusy}
                    onClick={() => void openPortal()}
                    type='button'
                  >
                    {t('projects.wallet.portalLink')}
                  </button>
                </>
              ) : null}
            </>
          }
          onPay={(value) => void pay(value)}
          paying={paying}
          title={t('projects.wallet.topUpTitle')}
        />
      ) : (
        <p className='pj-sub pj-topup-restricted'>
          {t('projects.wallet.topUpRestricted')}
        </p>
      )}
      {project.isOwner
        ? webExtensions().map((extension) =>
            extension.WalletAside ? (
              <extension.WalletAside key={extension.name} />
            ) : null,
          )
        : null}
      <div>
        <div>
          <div className='pj-tabs'>
            {(['ledger', 'topups', 'monthly'] as Tab[]).map((value) => (
              <button
                className='pj-tabs__btn'
                data-on={tab === value || undefined}
                data-owns-chrome=''
                key={value}
                onClick={() => setTab(value)}
                type='button'
              >
                {t(
                  `projects.wallet.tab.${value}${shared && value !== 'ledger' ? 'Shared' : ''}`,
                )}
              </button>
            ))}
          </div>
          {tab === 'topups' ? (
            <TopupTable
              rows={monthTopups}
              loading={topups.isLoading}
              own={!shared}
              projectId={project.current?.id ?? ''}
              highlightId={recordId}
            />
          ) : null}
          {tab === 'ledger' ? (
            <Card className='pj-table-card' pad={false}>
              <table className='pj-table'>
                <thead>
                  <tr>
                    <th>{t('projects.wallet.ledger.date')}</th>
                    <th className='pj-r'>
                      {t('projects.wallet.ledger.spend')}
                    </th>
                    <th className='pj-r'>
                      {t('projects.wallet.ledger.credit')}
                    </th>
                    <th className='pj-r'>
                      {t('projects.wallet.ledger.balance')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {ledgerRows.map((row) => (
                    <tr key={row.id}>
                      <td className='pj-mono pj-small'>
                        <Link
                          to={`/console/usage?tab=logs&project_id=${project.current?.id ?? ''}&start_timestamp=${dayjs(row.date).startOf('day').unix()}&end_timestamp=${dayjs(row.date).endOf('day').unix()}`}
                        >
                          {dayjs(row.date).format('MM-DD')}
                        </Link>
                      </td>
                      <td className='pj-r pj-num'>
                        {row.spend ? `−${row.spend.toFixed(2)}` : '—'}
                      </td>
                      <td className='pj-r pj-num'>
                        {row.credit ? `+${row.credit.toFixed(2)}` : '—'}
                      </td>
                      <td className='pj-r pj-num'>{row.balance.toFixed(2)}</td>
                    </tr>
                  ))}
                  {!ledgerRows.length ? (
                    <tr>
                      <td className='pj-muted' colSpan={4}>
                        {ledgerQuery.isLoading
                          ? t('projects.common.loading')
                          : ledgerQuery.isError
                            ? errorMessage(ledgerQuery.error)
                            : t('projects.wallet.ledger.empty')}
                      </td>
                    </tr>
                  ) : null}
                </tbody>
              </table>
            </Card>
          ) : null}
          {tab === 'monthly' ? (
            <Card className='pj-table-card' pad={false}>
              <table className='pj-table'>
                <thead>
                  <tr>
                    <th>
                      {shared
                        ? t('projects.wallet.monthly.person')
                        : t('projects.wallet.monthly.key')}
                    </th>
                    <th className='pj-r'>
                      {t('projects.wallet.monthly.spent')}
                    </th>
                    <th>{t('projects.wallet.monthly.budget')}</th>
                  </tr>
                </thead>
                <tbody>
                  {shared ? (
                    (members.data ?? []).map((m) => (
                      <tr key={m.user_id}>
                        <td>{m.username}</td>
                        <td className='pj-r pj-num'>
                          {formatUSD(m.month_spend_microusd)}
                        </td>
                        <td className='pj-muted'>
                          {m.budget_microusd === null
                            ? t('projects.budget.unlimited')
                            : formatUSDShort(m.budget_microusd)}
                        </td>
                      </tr>
                    ))
                  ) : (
                    <tr>
                      <td className='pj-muted' colSpan={3}>
                        {t('projects.wallet.monthly.ownHint')}
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
              <TableFoot
                left={t('projects.wallet.monthly.foot')}
                right={t('projects.wallet.usd')}
              />
            </Card>
          ) : null}
          <Card className='pj-footnote'>
            <strong>{t('projects.wallet.footnoteTitle')}</strong>
            <p>{t('projects.wallet.footnoteBody')}</p>
          </Card>
        </div>
      </div>
    </ConsolePage>
  );
}

function TopupTable({
  rows,
  loading,
  own,
  projectId,
  highlightId = '',
}: {
  rows: ProjectTopup[];
  loading: boolean;
  own: boolean;
  projectId: string;
  highlightId?: string;
}) {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const statusBadge = (row: ProjectTopup) => {
    if (row.payment_status === 'paid') return <PjBadge>Paid</PjBadge>;
    if (row.payment_status === 'pending')
      return (
        <PjBadge tone='warn'>{t('projects.wallet.status.pending')}</PjBadge>
      );
    return (
      <PjBadge tone='bad'>
        {t(`projects.wallet.status.${row.payment_status}`)}
      </PjBadge>
    );
  };
  return (
    <Card className='pj-table-card' pad={false}>
      <table className='pj-table'>
        <thead>
          <tr>
            <th>{t('projects.wallet.col.date')}</th>
            <th className='pj-r'>{t('projects.wallet.col.amount')}</th>
            <th>{t('projects.wallet.col.payer')}</th>
            <th>{t('projects.wallet.col.method')}</th>
            <th>Invoice</th>
            <th>{t('projects.wallet.col.status')}</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              className='pj-row'
              data-highlight={row.id === highlightId || undefined}
              key={row.id}
            >
              <td className='pj-mono pj-small pj-nowrap'>
                {formatDay(row.created_at)}
              </td>
              <td className='pj-r pj-num pj-strong'>
                {formatUSD(row.paid_amount_microusd)}
              </td>
              <td className='pj-nowrap'>
                {row.paid_by?.username ?? '—'}
                {own ? `（${t('projects.wallet.yourself')}）` : ''}
              </td>
              <td className='pj-muted pj-small pj-nowrap'>{row.method}</td>
              <td>
                {row.invoice_status === 'ready' && row.invoice ? (
                  <>
                    <span className='pj-mono pj-small'>
                      {row.invoice.number}
                    </span>
                    <span className='pj-sub'>
                      Bill to {row.invoice.bill_to?.name ?? '—'}
                    </span>
                  </>
                ) : row.invoice_status === 'pending' ? (
                  <span className='pj-sub'>
                    {t('projects.wallet.invoicePending')}
                  </span>
                ) : row.invoice_status === 'failed' ? (
                  <span className='pj-errt'>
                    {t('projects.wallet.invoiceFailed')}
                  </span>
                ) : (
                  <span className='pj-sub'>
                    {t('projects.wallet.invoiceLegacy')}
                  </span>
                )}
              </td>
              <td>{statusBadge(row)}</td>
              <td className='pj-r'>
                <span className='pj-ra'>
                  {/* The hosted Invoice page, not the PDF: Stripe prints
                      "Pay online" on the PDF of a paid Checkout Invoice. */}
                  <Button
                    disabled={
                      row.invoice_status !== 'ready' || !row.invoice?.hosted_url
                    }
                    render={
                      <a
                        aria-label='Invoice'
                        href={paymentDocumentPath(
                          projectId,
                          row.id,
                          'invoice_page',
                        )}
                        rel='noreferrer'
                        target='_blank'
                      />
                    }
                    size='sm'
                    variant='ghost'
                  >
                    Invoice
                  </Button>
                  <Button
                    disabled={!row.receipt_url}
                    render={
                      <a
                        aria-label='Receipt'
                        href={paymentDocumentPath(projectId, row.id, 'receipt')}
                        rel='noreferrer'
                        target='_blank'
                      />
                    }
                    size='sm'
                    variant='ghost'
                  >
                    Receipt
                  </Button>
                </span>
              </td>
            </tr>
          ))}
          {!rows.length ? (
            <tr>
              <td className='pj-muted' colSpan={7}>
                {loading
                  ? t('projects.common.loading')
                  : t('projects.wallet.noTopups')}
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>
      <TableFoot
        left={
          own
            ? t('projects.wallet.topupFootOwn')
            : t('projects.wallet.topupFoot')
        }
        right={t('projects.wallet.usd')}
      />
    </Card>
  );
}

/* --------------------------------------------------------- member wallet */

export function MyBudgetPage() {
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  const project = useProject();
  const budget = useMyBudget();
  const wallet = useProjectWallet({
    enabled: project.current?.status === 'active',
  });
  const b = budget.data;
  const remaining = b?.remaining_microusd ?? null;
  const pct =
    b && b.budget_microusd
      ? Math.round((b.spent_microusd / b.budget_microusd) * 100)
      : null;
  return (
    <ConsolePage
      appearance='review'
      className='pj-page pj-mybudget-page'
      title={t('projects.nav.myBudget')}
      description={t('projects.myBudget.lede', {
        project: project.currentName,
        owner: project.current?.ownerName ?? '',
      })}
    >
      <WalletPaymentRecord />
      <Ledger
        items={[
          {
            key: 'budget',
            label: t('projects.myBudget.budget'),
            value: b
              ? b.budget_microusd === null
                ? t('projects.budget.unlimited')
                : formatUSD(b.budget_microusd)
              : '—',
            hint: b
              ? t('projects.myBudget.resets', {
                  date: formatMonthDay(b.resets_at),
                })
              : '',
          },
          {
            key: 'spent',
            label: t('projects.member.spent'),
            value: formatUSD(b?.spent_microusd ?? 0),
            hint: b
              ? t('projects.member.spentHint', {
                  pct: pct ?? 0,
                  daily: formatUSD(b.daily_avg_microusd),
                })
              : '',
            tone:
              pct !== null && pct >= 100
                ? 'bad'
                : pct !== null && b && pct >= b.warning_pct
                  ? 'warn'
                  : undefined,
          },
          {
            key: 'left',
            label: t('projects.member.canSpend'),
            value: b
              ? b.budget_microusd === null
                ? '∞'
                : formatUSD(remaining)
              : '—',
            hint:
              b && b.budget_microusd !== null
                ? t('projects.myBudget.leftHint', { pct: b.warning_pct })
                : '',
          },
          {
            key: 'balance',
            label: t('projects.wallet.projectBalance'),
            value: formatUSD(wallet.data?.balance_microusd),
            hint: t('projects.myBudget.balanceHint', {
              owner: project.current?.ownerName ?? '',
              days: wallet.data?.days_left ?? '—',
            }),
          },
        ]}
      />
      <Card>
        <SectionHead
          sub={t('projects.myBudget.noTopupSub')}
          title={t('projects.myBudget.noTopupTitle')}
        />
        <p className='pj-muted'>{t('projects.myBudget.noTopupBody')}</p>
        <div className='pj-mt-s'>
          <Button render={<Link to='/console' />} size='sm' variant='secondary'>
            {t('projects.myBudget.toOverview')}
          </Button>
        </div>
      </Card>
    </ConsolePage>
  );
}

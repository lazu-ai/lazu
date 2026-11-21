/* The wallet's 账单 tab. The list view is six months at a glance: a bar per
 * month's spend, a row per month that moved, and the top-up records of the
 * same six months. A month opens its own view with a bar per day and a row
 * per day that moved; a day opens one centered dialog with that day's spend
 * by model, read from the usage the dialog's figures add up to.
 *
 * 充值记录 rows open in place onto their numbers and documents; a link to
 * one payment (?top_up_id=, from the Stripe return and the payment e-mails)
 * opens the same list as a dialog with that payment open. */
import { useQueries, useQuery } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { ChevronRight, X } from 'lucide-react';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  billMonthKeys,
  dayBarHeight,
  dayRange,
  downloadCsv,
  monthBarHeight,
  monthRange,
  summarizeMonth,
  walletCsv,
  type WalletLedgerRange,
  type WalletMonth,
} from './billMonths';
import { resolveTopupCreditUSD, resolveTopupPaidUSD } from './walletAmounts';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { formatCount } from '../../components/ui/metric-format';
import { ModalFrame, ModalTitle } from '../../components/ui/modal';
import { Pagination } from '../../components/ui/pagination';
import { PendingValue } from '../../components/ui/pending';
import { TopupRecord } from '../../components/ui/topup-record';
import { accountPaymentQueryOptions } from '../../features/projects/paymentRecord';
import { API } from '../../helpers/api';
import { getMicroUSDPerUSD } from '../../helpers/render';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import type { TopupRecordData } from '../../features/projects/paymentRecord';

const ns = 'page.console.wallet.bill';

/* The day table shows this many days before 显示全部. */
const DAYS_SHOWN = 10;
const RECORDS_PAGE = 10;

export type WalletTopup = Record<string, unknown> & {
  amount?: number;
  complete_time?: number;
  create_time?: number;
  credited_microusd?: number;
  id?: number | string;
  payment_method?: string;
  public_id?: string;
  status?: string;
  trade_no?: string;
};

type DayUsage = {
  by_model?: Array<{
    amount_microusd?: number;
    completion_tokens?: number;
    name?: string;
    prompt_tokens?: number;
    request_count?: number;
  }>;
  totals?: { amount_microusd?: number; request_count?: number };
};

const money = (value: number) =>
  value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
const plus = (value: number) => (value ? `+${money(value)}` : '');
const minus = (value: number) => (value ? `−${money(value)}` : '0.00');

function useMonthLabels() {
  const { i18n, t } = useTranslation();
  return useMemo(() => {
    const long = new Intl.DateTimeFormat(i18n.language, { month: 'long' });
    const short = new Intl.DateTimeFormat(i18n.language, { month: 'short' });
    return {
      full: (key: string) => {
        const date = dayjs(`${key}-01`);
        return t(`${ns}.monthFull`, {
          month: date.month() + 1,
          monthName: long.format(date.toDate()),
          year: date.year(),
        });
      },
      short: (key: string) => {
        const date = dayjs(`${key}-01`);
        return t(`${ns}.monthShort`, {
          month: date.month() + 1,
          monthName: short.format(date.toDate()),
        });
      },
    };
  }, [i18n.language, t]);
}

const dayLabel = (date: string) => dayjs(date).format('MM.DD');

/* Ledger months for the bill. Past months never change, so they stay
   cached for the session; the current month refetches as usual. */
function useWalletMonths(now: dayjs.Dayjs) {
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const keys = billMonthKeys(now);
  const results = useQueries({
    queries: keys.map((key) => {
      const range = monthRange(key, now);
      return {
        queryKey: [
          ...queryKeys.console.detail('wallet', 'ledger'),
          key,
          range.end,
          timezone,
        ],
        queryFn: async () => {
          const res = await API.get('/api/user/wallet/ledger', {
            params: {
              start_timestamp: range.start,
              end_timestamp: range.end,
              timezone,
              timezone_offset_minutes: new Date().getTimezoneOffset(),
            },
            skipErrorHandler: true,
          });
          return (unwrap<WalletLedgerRange>(res) || {}) as WalletLedgerRange;
        },
        staleTime: range.isCurrent ? 30_000 : Number.POSITIVE_INFINITY,
      };
    }),
  });
  const perUSD = getMicroUSDPerUSD();
  const months = keys.map((key, index) =>
    results[index].data
      ? summarizeMonth(key, results[index].data, perUSD)
      : null,
  );
  return {
    error: results.some((result) => result.isError),
    keys,
    loading: results.some((result) => result.isPending),
    months,
    refetch: () => results.forEach((result) => void result.refetch()),
  };
}

/* Without a range the list is the server's default: the last 30 days. */
function useTopups(start: number | null, end: number | null, page: number) {
  return useQuery({
    queryKey: [
      ...queryKeys.console.detail('wallet', 'history'),
      'self',
      start,
      end,
      page,
    ],
    queryFn: async () => {
      const res = await API.get('/api/user/topup/self', {
        params: {
          p: page,
          page_size: RECORDS_PAGE,
          ...(start && end
            ? { end_timestamp: end, start_timestamp: start }
            : {}),
        },
        skipErrorHandler: true,
      });
      const data = unwrap<{ items?: unknown; total?: number }>(res);
      return {
        items: (Array.isArray(data?.items) ? data.items : []) as WalletTopup[],
        total: Number(data?.total || 0),
      };
    },
  });
}

type TopupLine = {
  creditUSD: number;
  id: string;
  method: string;
  paidUSD: number;
  status: string;
  when: number;
};

function lineFromTopup(item: WalletTopup): TopupLine {
  const status = String(item.status || 'pending');
  const settled = status === 'success';
  return {
    creditUSD: resolveTopupCreditUSD(item, getMicroUSDPerUSD()),
    id: String(item.public_id || ''),
    method: String(item.payment_method || ''),
    paidUSD: settled ? resolveTopupPaidUSD(item) : 0,
    status,
    when: Number((settled && item.complete_time) || item.create_time || 0),
  };
}

/* A linked payment older than the list's first page is read from its own
   record and shown first. */
function lineFromRecord(record: TopupRecordData): TopupLine {
  const perUSD = getMicroUSDPerUSD();
  const status =
    record.payment_status === 'paid'
      ? 'success'
      : record.payment_status === 'failed'
        ? 'pending'
        : record.payment_status;
  return {
    creditUSD: record.credited_microusd / perUSD,
    id: record.id,
    method: record.payment_method,
    paidUSD: status === 'success' ? record.paid_amount_microusd / perUSD : 0,
    status,
    when: record.paid_at || record.created_at,
  };
}

function TopupRows({
  documentError,
  lines,
  onToggle,
  openId,
}: {
  documentError?: boolean;
  lines: TopupLine[];
  onToggle: (id: string) => void;
  openId: string | null;
}) {
  const { t } = useTranslation();
  const statuses: Record<string, { label: string; tone?: 'success' }> = {
    canceled: { label: t(`${ns}.status.canceled`) },
    expired: { label: t(`${ns}.status.expired`) },
    pending: { label: t(`${ns}.status.pending`) },
    success: { label: t(`${ns}.status.success`), tone: 'success' },
  };
  return (
    <table className='wal-table wal-topups'>
      <thead>
        <tr>
          <th className='wal-topups__toggle'>
            <span className='sr-only'>{t(`${ns}.col.open`)}</span>
          </th>
          <th>{t(`${ns}.col.time`)}</th>
          <th>{t(`${ns}.col.amount`)}</th>
          <th>{t(`${ns}.col.method`)}</th>
          <th className='wal-table__last'>{t(`${ns}.col.status`)}</th>
        </tr>
      </thead>
      {lines.map((line) => {
        const state = statuses[line.status] || { label: line.status };
        const settled = line.status === 'success';
        const open = Boolean(line.id) && openId === line.id;
        const detailId = `wal-topup-${line.id}`;
        const credited =
          settled && Math.abs(line.creditUSD - line.paidUSD) >= 0.005;
        return (
          <tbody
            className='wal-topups__item'
            data-dim={settled ? undefined : ''}
            data-open={open || undefined}
            key={line.id || line.when}
          >
            <tr>
              <td className='wal-topups__toggle'>
                {line.id ? (
                  <button
                    aria-controls={open ? detailId : undefined}
                    aria-expanded={open}
                    aria-label={t(`${ns}.col.open`)}
                    className='wal-icon-button wal-topups__chevron'
                    onClick={() => onToggle(line.id)}
                    type='button'
                  >
                    <ChevronRight aria-hidden='true' size={15} />
                  </button>
                ) : null}
              </td>
              <td>
                {line.when
                  ? dayjs.unix(line.when).format('YYYY-MM-DD HH:mm')
                  : '—'}
              </td>
              <td>
                <span className='wal-topups__amount'>
                  ${money(settled ? line.paidUSD : line.creditUSD)}
                </span>
                {credited ? (
                  <span className='wal-topups__credited'>
                    {t(`${ns}.creditedTo`, {
                      amount: `$${money(line.creditUSD)}`,
                    })}
                  </span>
                ) : null}
              </td>
              <td>
                {line.method
                  ? t(`page.console.wallet.record.method.${line.method}`, {
                      defaultValue: line.method,
                    })
                  : '—'}
              </td>
              <td className='wal-table__last'>
                <Badge variant={state.tone ?? 'neutral'}>{state.label}</Badge>
              </td>
            </tr>
            {open ? (
              <tr className='wal-topups__detail' id={detailId}>
                <td />
                <td className='wal-table__last' colSpan={4}>
                  <TopupRecord
                    documentError={documentError}
                    topupId={line.id}
                    variant='row'
                  />
                </td>
              </tr>
            ) : null}
          </tbody>
        );
      })}
    </table>
  );
}

function TopupRecords({ end, start }: { end: number; start: number }) {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [openId, setOpenId] = useState<string | null>(null);
  const records = useTopups(start, end, page);
  const items = records.data?.items || [];
  return (
    <section className='wal-card wal-records' aria-labelledby='wal-records'>
      <h2 className='wal-records__title' id='wal-records'>
        {t(`${ns}.records`)}
      </h2>
      {records.isError ? (
        <p className='wal-empty'>
          {t(`${ns}.recordsError`)}{' '}
          <button
            className='wal-link'
            onClick={() => void records.refetch()}
            type='button'
          >
            {t('common.retry', { defaultValue: '重试' })}
          </button>
        </p>
      ) : records.isPending ? (
        <p className='wal-empty'>
          <PendingValue chars={18} />
        </p>
      ) : items.length === 0 ? (
        <p className='wal-empty'>{t(`${ns}.recordsEmpty`)}</p>
      ) : (
        <TopupRows
          lines={items.map(lineFromTopup)}
          onToggle={(id) =>
            setOpenId((current) => (current === id ? null : id))
          }
          openId={openId}
        />
      )}
      {(records.data?.total || 0) > RECORDS_PAGE ? (
        <div className='wal-records__pager'>
          <Pagination
            currentPage={page}
            onPageChange={setPage}
            pageSize={RECORDS_PAGE}
            total={records.data?.total || 0}
          />
        </div>
      ) : null}
    </section>
  );
}

/* 充值记录 as a dialog, over whichever tab the link landed on: the last 30
   days, with the linked payment open (and first, when it is older). */
export function TopupRecordsDialog({
  documentError,
  onClose,
  topupId,
}: {
  documentError: boolean;
  onClose: () => void;
  topupId: string;
}) {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const [openId, setOpenId] = useState<string | null>(topupId);
  const records = useTopups(null, null, page);
  const linked = useQuery(accountPaymentQueryOptions(topupId));
  const items = records.data?.items || [];
  const lines = items.map(lineFromTopup);
  const listed = lines.some((line) => line.id === topupId);
  /* The linked payment never depends on the list: unless it is one of the
     listed rows, it is shown first, as a row once its record arrives, and
     on its own (with its own wait, 找不到, 切换账号 and 重试) until then, or
     when the list itself fails. */
  const showLinked = page === 1 && !records.isPending && !listed;
  const pinned =
    showLinked && records.isSuccess && linked.data
      ? lineFromRecord(linked.data)
      : null;
  const lone = showLinked && !pinned;
  const total = (records.data?.total || 0) + (pinned ? 1 : 0);
  const shown = pinned ? [pinned, ...lines] : lines;

  return (
    <ModalFrame
      className='wal-topups-dialog'
      hideClose
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      open
      sheet='full'
    >
      <header className='wal-topups-dialog__head'>
        <ModalTitle className='wal-day__title'>{t(`${ns}.records`)}</ModalTitle>
        <button
          aria-label={t('common.close', { defaultValue: '关闭' })}
          className='wal-icon-button'
          onClick={onClose}
          type='button'
        >
          <X aria-hidden='true' size={16} />
        </button>
      </header>
      <div className='wal-topups-dialog__body'>
        {lone ? (
          <div className='wal-topups-dialog__lone'>
            <TopupRecord
              documentError={documentError}
              topupId={topupId}
              variant='row'
            />
          </div>
        ) : null}
        {records.isError ? (
          <p className='wal-empty'>
            {t(`${ns}.recordsError`)}{' '}
            <button
              className='wal-link'
              onClick={() => void records.refetch()}
              type='button'
            >
              {t('common.retry', { defaultValue: '重试' })}
            </button>
          </p>
        ) : records.isPending ? (
          <p className='wal-empty'>
            <PendingValue chars={18} />
          </p>
        ) : shown.length === 0 ? (
          lone ? null : (
            <p className='wal-empty'>{t(`${ns}.recordsEmpty`)}</p>
          )
        ) : (
          <TopupRows
            documentError={documentError}
            lines={shown}
            onToggle={(id) =>
              setOpenId((current) => (current === id ? null : id))
            }
            openId={openId}
          />
        )}
      </div>
      <footer className='wal-topups-dialog__foot'>
        <span className='wal-label'>
          {records.isSuccess ? t(`${ns}.recordsCount`, { count: total }) : ''}
        </span>
        {(records.data?.total || 0) > RECORDS_PAGE ? (
          <Pagination
            currentPage={page}
            onPageChange={setPage}
            pageSize={RECORDS_PAGE}
            total={records.data?.total || 0}
          />
        ) : null}
      </footer>
    </ModalFrame>
  );
}

type WalletBillProps = {
  balanceUSD: number | null;
  balancePending: boolean;
  day: string | null;
  month: string | null;
  now: dayjs.Dayjs;
  onDay: (day: string | null) => void;
  onMonth: (month: string | null) => void;
};

export function WalletBill(props: WalletBillProps) {
  const { t } = useTranslation();
  const ledger = useWalletMonths(props.now);
  const opened =
    props.month && ledger.keys.includes(props.month) ? props.month : null;

  return (
    <div className='wal-bill'>
      {ledger.error ? (
        <p className='wal-card wal-empty wal-bill__error' role='alert'>
          {t(`${ns}.loadError`)}{' '}
          <button className='wal-link' onClick={ledger.refetch} type='button'>
            {t('common.retry', { defaultValue: '重试' })}
          </button>
        </p>
      ) : null}
      {opened ? (
        <MonthView {...props} ledger={ledger} month={opened} />
      ) : (
        <ListView {...props} ledger={ledger} />
      )}
      {props.day ? (
        <DayDialog day={props.day} now={props.now} onDay={props.onDay} />
      ) : null}
    </div>
  );
}

type Ledger = ReturnType<typeof useWalletMonths>;

function ListView({
  balanceUSD,
  balancePending,
  ledger,
  now,
  onMonth,
}: WalletBillProps & { ledger: Ledger }) {
  const { t } = useTranslation();
  const labels = useMonthLabels();
  const current = ledger.keys[0];
  const ascending = [...ledger.keys].reverse();
  const max = Math.max(0, ...ledger.months.map((month) => month?.spend || 0));
  const rows = ledger.months.filter(
    (month): month is WalletMonth =>
      Boolean(month) &&
      (month!.key === current || month!.credit > 0 || month!.spend > 0),
  );
  const oldest = monthRange(ledger.keys[ledger.keys.length - 1], now);
  const newest = monthRange(current, now);

  const exportMonths = () =>
    downloadCsv(
      `lazu-wallet-${ascending[0]}-${current}.csv`,
      walletCsv(
        [
          t(`${ns}.csv.month`),
          t(`${ns}.csv.opening`),
          t(`${ns}.csv.credit`),
          t(`${ns}.csv.spend`),
          t(`${ns}.csv.closing`),
        ],
        ledger.months
          .filter((month): month is WalletMonth => Boolean(month))
          .reverse()
          .map((month) => [
            month.key,
            month.opening.toFixed(2),
            month.credit.toFixed(2),
            month.spend.toFixed(2),
            month.closing.toFixed(2),
          ]),
      ),
    );

  return (
    <>
      <div className='wal-bill__head'>
        <span className='wal-bill__balance'>
          <span className='wal-label'>{t(`${ns}.balance`)}</span>
          {balancePending ? (
            <PendingValue chars={6} />
          ) : (
            <span className='wal-money wal-bill__balance-value'>
              {balanceUSD === null ? '—' : `$${money(balanceUSD)}`}
            </span>
          )}
        </span>
        <Button
          disabled={ledger.loading}
          onClick={exportMonths}
          variant='secondary'
        >
          {t(`${ns}.export`)}
        </Button>
      </div>

      <section className='wal-card wal-months' aria-labelledby='wal-months'>
        <div className='wal-months__head'>
          <h2 id='wal-months'>{t(`${ns}.monthly`)}</h2>
          <span className='wal-label'>
            {t(`${ns}.lastMonths`, { count: 6 })}
          </span>
        </div>
        <div className='wal-months__bars'>
          {ascending.map((key) => {
            const month = ledger.months[ledger.keys.indexOf(key)];
            const spend = month?.spend || 0;
            return (
              <button
                aria-label={`${labels.full(key)} ${month ? `$${money(spend)}` : ''}`}
                className='wal-months__bar'
                data-current={key === current ? '' : undefined}
                data-empty={spend > 0 ? undefined : ''}
                key={key}
                onClick={() => onMonth(key)}
                title={`${labels.full(key)} $${money(spend)}`}
                type='button'
              >
                <span className='wal-months__value'>
                  {spend > 0 ? `$${money(spend)}` : ''}
                </span>
                <span
                  className='wal-months__fill'
                  style={{
                    height: `${month ? monthBarHeight(spend, max) : 2}%`,
                  }}
                />
              </button>
            );
          })}
        </div>
        <div aria-hidden='true' className='wal-months__axis'>
          {ascending.map((key) => (
            <span key={key}>{labels.short(key)}</span>
          ))}
        </div>
      </section>

      <div className='wal-card wal-table-card'>
        <table className='wal-table'>
          <thead>
            <tr>
              <th className='wal-table__first'>{t(`${ns}.col.month`)}</th>
              <th className='wal-num'>{t(`${ns}.col.credit`)}</th>
              <th className='wal-num'>{t(`${ns}.col.spend`)}</th>
              <th className='wal-num'>{t(`${ns}.col.monthEnd`)}</th>
              <th aria-hidden='true' className='wal-table__chevron' />
            </tr>
          </thead>
          <tbody>
            {ledger.loading && rows.length === 0 ? (
              <tr>
                <td className='wal-empty' colSpan={5}>
                  <PendingValue chars={18} />
                </td>
              </tr>
            ) : (
              rows.map((month) => (
                <tr
                  className='wal-table__row'
                  key={month.key}
                  onClick={() => onMonth(month.key)}
                >
                  <td className='wal-table__first wal-table__strong'>
                    <button
                      className='wal-row-link'
                      onClick={(event) => {
                        event.stopPropagation();
                        onMonth(month.key);
                      }}
                      type='button'
                    >
                      {labels.full(month.key)}
                    </button>
                    {month.key === current ? (
                      <Badge className='wal-badge-current' variant='info'>
                        {t(`${ns}.thisMonth`)}
                      </Badge>
                    ) : null}
                  </td>
                  <td className='wal-num wal-credit'>{plus(month.credit)}</td>
                  <td className='wal-num'>{minus(month.spend)}</td>
                  <td className='wal-num'>{money(month.closing)}</td>
                  <td aria-hidden='true' className='wal-table__chevron'>
                    ›
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <TopupRecords end={newest.end} start={oldest.start} />
    </>
  );
}

function MonthView({
  ledger,
  month: key,
  now,
  onDay,
  onMonth,
}: Omit<WalletBillProps, 'month'> & { ledger: Ledger; month: string }) {
  const { t } = useTranslation();
  const labels = useMonthLabels();
  const [showAll, setShowAll] = useState(false);
  const month = ledger.months[ledger.keys.indexOf(key)];
  const range = monthRange(key, now);
  const first = dayjs(`${key}-01`);
  const length = first.daysInMonth();
  const today = now.format('YYYY-MM-DD');
  const spendByDay = new Map(
    (month?.days || []).map((day) => [day.date, day.spend]),
  );
  const max = Math.max(0, ...spendByDay.values());
  const days = month?.days || [];
  const shown = showAll ? days : days.slice(0, DAYS_SHOWN);
  const middle = Math.min(15, length);

  const exportMonth = () =>
    downloadCsv(
      `lazu-wallet-${key}.csv`,
      walletCsv(
        [
          t(`${ns}.csv.date`),
          t(`${ns}.csv.credit`),
          t(`${ns}.csv.spend`),
          t(`${ns}.csv.balance`),
        ],
        [...days]
          .reverse()
          .map((day) => [
            day.date,
            day.credit.toFixed(2),
            day.spend.toFixed(2),
            day.balance.toFixed(2),
          ]),
      ),
    );

  return (
    <>
      <div className='wal-bill__head'>
        <div className='wal-bill__title'>
          <Button onClick={() => onMonth(null)} variant='ghost'>
            {`← ${t(`${ns}.allMonths`)}`}
          </Button>
          <h2>{labels.full(key)}</h2>
        </div>
        <Button disabled={!month} onClick={exportMonth} variant='secondary'>
          {t(`${ns}.exportMonth`)}
        </Button>
      </div>

      <section className='wal-card wal-days' aria-label={labels.full(key)}>
        <dl className='wal-days__sum'>
          <div>
            <dt>{t(`${ns}.opening`)}</dt>
            <dd>{month ? money(month.opening) : '—'}</dd>
          </div>
          <div>
            <dt>{t(`${ns}.credit`)}</dt>
            <dd className='wal-credit'>
              {month ? plus(month.credit) || '0.00' : '—'}
            </dd>
          </div>
          <div>
            <dt>{t(`${ns}.spend`)}</dt>
            <dd>{month ? minus(month.spend) : '—'}</dd>
          </div>
          <div>
            <dt>{t(`${ns}.closing`)}</dt>
            <dd className='wal-days__closing'>
              {month ? money(month.closing) : '—'}
            </dd>
          </div>
        </dl>
        <div className='wal-days__bars'>
          {Array.from({ length }, (_, index) => {
            const date = first.add(index, 'day');
            const dateKey = date.format('YYYY-MM-DD');
            const future = date.isAfter(now, 'day');
            const spend = spendByDay.get(dateKey) || 0;
            return (
              <button
                aria-label={`${dayLabel(dateKey)} $${money(spend)}`}
                className='wal-days__bar'
                data-future={future ? '' : undefined}
                data-today={dateKey === today ? '' : undefined}
                disabled={future}
                key={dateKey}
                onClick={() => onDay(dateKey)}
                title={`${dayLabel(dateKey)} $${money(spend)}`}
                type='button'
              >
                <span
                  style={{
                    height: `${future ? 3 : dayBarHeight(spend, max)}%`,
                  }}
                />
              </button>
            );
          })}
        </div>
        <div aria-hidden='true' className='wal-days__axis'>
          <span>{first.format('MM.DD')}</span>
          <span>{first.date(middle).format('MM.DD')}</span>
          <span>{first.date(length).format('MM.DD')}</span>
        </div>
      </section>

      <div className='wal-card wal-table-card'>
        <table className='wal-table'>
          <thead>
            <tr>
              <th className='wal-table__first'>{t(`${ns}.col.date`)}</th>
              <th>{t(`${ns}.col.credit`)}</th>
              <th className='wal-num'>{t(`${ns}.col.spend`)}</th>
              <th className='wal-num'>{t(`${ns}.col.balance`)}</th>
              <th aria-hidden='true' className='wal-table__chevron' />
            </tr>
          </thead>
          <tbody>
            {!month ? (
              <tr>
                <td className='wal-empty' colSpan={5}>
                  <PendingValue chars={18} />
                </td>
              </tr>
            ) : shown.length === 0 ? (
              <tr>
                <td className='wal-empty' colSpan={5}>
                  {t(`${ns}.daysEmpty`)}
                </td>
              </tr>
            ) : (
              shown.map((day) => (
                <tr
                  className='wal-table__row'
                  data-today={day.date === today ? '' : undefined}
                  key={day.date}
                  onClick={() => onDay(day.date)}
                >
                  <td className='wal-table__first'>
                    <button
                      className='wal-row-link'
                      onClick={(event) => {
                        event.stopPropagation();
                        onDay(day.date);
                      }}
                      type='button'
                    >
                      {dayLabel(day.date)}
                      {day.date === today ? ` ${t(`${ns}.today`)}` : ''}
                    </button>
                  </td>
                  <td className='wal-credit'>{plus(day.credit)}</td>
                  <td className='wal-num'>{minus(day.spend)}</td>
                  <td className='wal-num'>{money(day.balance)}</td>
                  <td aria-hidden='true' className='wal-table__chevron'>
                    ›
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
        {!showAll && days.length > DAYS_SHOWN ? (
          <div className='wal-table__more'>
            <button
              className='wal-link'
              onClick={() => setShowAll(true)}
              type='button'
            >
              {t(`${ns}.showAllDays`, { count: days.length })}
            </button>
          </div>
        ) : null}
      </div>

      <TopupRecords end={range.end} start={range.start} />
    </>
  );
}

function DayDialog({
  day,
  now,
  onDay,
}: {
  day: string;
  now: dayjs.Dayjs;
  onDay: (day: string | null) => void;
}) {
  const { t } = useTranslation();
  const range = dayRange(day);
  const usage = useQuery({
    queryKey: [...queryKeys.dashboard.summary(), 'wallet-day', day],
    queryFn: async () => {
      const res = await API.get('/api/data/dashboard/self', {
        params: { end_timestamp: range.end, start_timestamp: range.start },
        skipErrorHandler: true,
      });
      return (unwrap<DayUsage | null>(res) || {}) as DayUsage;
    },
  });
  const perUSD = getMicroUSDPerUSD();
  const total = Number(usage.data?.totals?.amount_microusd || 0) / perUSD;
  const requests = Number(usage.data?.totals?.request_count || 0);
  const models = (usage.data?.by_model || []).filter(
    (row) =>
      Number(row.request_count || 0) > 0 ||
      Number(row.amount_microusd || 0) > 0,
  );
  const date = dayjs(day);
  const earliest = now.startOf('month').subtract(5, 'month');
  const isToday = date.isSame(now, 'day');
  const logs = `/console/usage?tab=logs&start_timestamp=${range.start}&end_timestamp=${range.end}`;

  return (
    <ModalFrame
      className='wal-day'
      hideClose
      onOpenChange={(open) => {
        if (!open) onDay(null);
      }}
      open
      sheet='full'
    >
      <header className='wal-day__head'>
        <div>
          <ModalTitle className='wal-day__title'>
            {dayLabel(day)}
            {isToday ? ` ${t(`${ns}.today`)}` : ''}
          </ModalTitle>
          <p className='wal-label'>
            {usage.isPending ? (
              <PendingValue chars={8} />
            ) : usage.isError ? (
              '—'
            ) : (
              t(`${ns}.requests`, { count: requests })
            )}
          </p>
        </div>
        <div className='wal-day__total'>
          <span className='wal-money'>
            {usage.isPending
              ? ''
              : usage.isError
                ? '$ —'
                : `${total ? '−' : ''}$${money(total)}`}
          </span>
          <button
            aria-label={t('common.close', { defaultValue: '关闭' })}
            className='wal-icon-button'
            onClick={() => onDay(null)}
            type='button'
          >
            <X aria-hidden='true' size={16} />
          </button>
        </div>
      </header>
      <div className='wal-day__body'>
        <table className='wal-table'>
          <thead>
            <tr>
              <th className='wal-table__first'>{t(`${ns}.col.model`)}</th>
              <th className='wal-num'>{t(`${ns}.col.requests`)}</th>
              <th className='wal-num'>{t(`${ns}.col.tokens`)}</th>
              <th className='wal-num wal-table__last'>
                {t(`${ns}.col.amount`)}
              </th>
            </tr>
          </thead>
          <tbody>
            {usage.isError ? (
              <tr>
                <td className='wal-empty' colSpan={4}>
                  {t(`${ns}.dayError`)}{' '}
                  <button
                    className='wal-link'
                    onClick={() => void usage.refetch()}
                    type='button'
                  >
                    {t('common.retry', { defaultValue: '重试' })}
                  </button>
                </td>
              </tr>
            ) : usage.isPending ? (
              <tr>
                <td className='wal-empty' colSpan={4}>
                  <PendingValue chars={18} />
                </td>
              </tr>
            ) : models.length === 0 ? (
              <tr>
                <td className='wal-empty' colSpan={4}>
                  {t(`${ns}.dayEmpty`)}
                </td>
              </tr>
            ) : (
              models.map((row) => (
                <tr key={row.name}>
                  <td className='wal-table__first wal-mono wal-day__model'>
                    {row.name}
                  </td>
                  <td className='wal-num wal-sans'>
                    {Number(row.request_count || 0).toLocaleString()}
                  </td>
                  <td className='wal-num wal-muted'>
                    {formatCount(
                      Number(row.prompt_tokens || 0) +
                        Number(row.completion_tokens || 0),
                    )}
                  </td>
                  <td className='wal-num wal-table__last'>
                    {money(Number(row.amount_microusd || 0) / perUSD)}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
      <footer className='wal-day__foot'>
        <Link className='wal-link' to={logs}>
          {t(`${ns}.logs`)}
        </Link>
        <span className='wal-day__nav'>
          <Button
            disabled={!date.isAfter(earliest, 'day')}
            onClick={() => onDay(date.subtract(1, 'day').format('YYYY-MM-DD'))}
            size='sm'
            variant='secondary'
          >
            {t(`${ns}.prevDay`)}
          </Button>
          <Button
            disabled={isToday || date.isAfter(now, 'day')}
            onClick={() => onDay(date.add(1, 'day').format('YYYY-MM-DD'))}
            size='sm'
            variant='secondary'
          >
            {t(`${ns}.nextDay`)}
          </Button>
        </span>
      </footer>
    </ModalFrame>
  );
}

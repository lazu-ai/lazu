/* Every account's top-up orders, for site administrators: search by order
 * number, scope to one owner (?owner=), and 确认到账 a pending order after
 * its own confirmation. The reader's own records live on the 账单 tab. */
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Search } from 'lucide-react';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  resolveTopupCreditUSD,
  resolveTopupEffectiveTimestamp,
  resolveTopupPaidUSD,
} from './walletAmounts';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { DataTableShell } from '../../components/ui/data-table-shell';
import { EmptyState } from '../../components/ui/empty-state';
import { Field, FieldMessage, Label } from '../../components/ui/field';
import { Input } from '../../components/ui/input';
import { Modal } from '../../components/ui/modal';
import { Pagination } from '../../components/ui/pagination';
import { notify } from '../../components/ui/toast';
import { API } from '../../helpers/api';
import { getMicroUSDPerUSD } from '../../helpers/render';
import { apiErrorHandler, errorMessage, unwrap } from '../../lib/api-error';
import { formatTimestamp } from '../../lib/date';
import { queryKeys } from '../../lib/query-keys';
import type { WalletTopup } from './WalletBill';
import type { ColumnDef } from '@tanstack/react-table';
import type { ChangeEvent } from 'react';

const topupNs = 'page.console.topup';

const STATUS_VARIANTS: Record<string, 'danger' | 'success' | 'warning'> = {
  expired: 'danger',
  pending: 'warning',
  success: 'success',
};

const formatCurrency = (value: number) =>
  value.toLocaleString(undefined, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });

const getSearchableHistoryKeyword = (value: unknown) => {
  const trimmed = String(value || '').trim();
  const effective = trimmed.replace(/[%_!]/g, '').trim();
  return effective.length >= 2 ? trimmed : '';
};

type RootTopupHistoryProps = {
  onOpenChange: (open: boolean) => void;
  open: boolean;
  owner: string;
};

export function RootTopupHistory({
  onOpenChange,
  open,
  owner,
}: RootTopupHistoryProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [keyword, setKeyword] = useState('');
  const [completeTradeNo, setCompleteTradeNo] = useState<string | null>(null);
  const searchKeyword = getSearchableHistoryKeyword(keyword);
  const keywordTooShort = keyword.trim().length > 0 && !searchKeyword;

  const history = useQuery({
    queryKey: [
      ...queryKeys.console.detail('wallet', 'history'),
      'all',
      page,
      pageSize,
      searchKeyword,
      owner,
    ],
    enabled: open,
    queryFn: async () => {
      // The failure is shown under the search field, so the global toast
      // would say it twice.
      const res = await API.get('/api/user/topup', {
        params: {
          p: page,
          page_size: pageSize,
          ...(searchKeyword ? { keyword: searchKeyword } : {}),
          ...(owner ? { owner } : {}),
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

  const complete = useMutation<void, Error, string>({
    mutationFn: async (tradeNo) => {
      unwrap(await API.post('/api/user/topup/complete', { trade_no: tradeNo }));
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

  const columns = useMemo<ColumnDef<WalletTopup, unknown>[]>(
    () => [
      {
        header: t(`${topupNs}.history.orderNo`),
        accessorKey: 'trade_no',
        // Long order numbers end in an ellipsis so the amount and status
        // columns stay in view; the full number is on hover and in search.
        cell: ({ getValue }) => (
          <span
            className='block max-w-[16ch] truncate font-mono text-xs'
            title={String(getValue() || '')}
          >
            {String(getValue() || '-')}
          </span>
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
      {
        header: t(`${topupNs}.history.action`),
        id: 'action',
        cell: ({ row }) =>
          row.original.status === 'pending' ? (
            <Button
              loading={complete.isPending}
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
      },
    ],
    [complete.isPending, t],
  );

  return (
    <>
      <Modal
        className='wal-history-dialog'
        open={open}
        onOpenChange={onOpenChange}
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
                    setKeyword(event.target.value);
                    setPage(1);
                  }}
                  placeholder={t(`${topupNs}.history.searchPlaceholder`)}
                  value={keyword}
                />
              </div>
            </Field>
            {keywordTooShort ? (
              <FieldMessage>
                {t(`${topupNs}.history.searchMinimum`)}
              </FieldMessage>
            ) : null}
            {history.error ? (
              <FieldMessage error>
                {errorMessage(history.error, t(`${topupNs}.error.loadHistory`))}
              </FieldMessage>
            ) : null}
            <DataTableShell
              columns={columns}
              data={history.data?.items || []}
              empty={<EmptyState title={t(`${topupNs}.history.empty`)} />}
              loading={history.isLoading || history.isFetching}
              rowKey={(row) => String(row.id || row.trade_no || '')}
            />
            {(history.data?.total || 0) > pageSize ? (
              <Pagination
                currentPage={page}
                onPageChange={setPage}
                onPageSizeChange={(size) => {
                  setPageSize(size);
                  setPage(1);
                }}
                pageSize={pageSize}
                showSizeChanger
                total={history.data?.total || 0}
              />
            ) : null}
          </div>
        }
      />
      <Modal
        open={completeTradeNo !== null}
        busy={complete.isPending}
        onOpenChange={(next) => {
          if (!next) setCompleteTradeNo(null);
        }}
        title={t(`${topupNs}.history.complete`)}
        body={<p>{t(`${topupNs}.history.completeConfirm`)}</p>}
        footer={
          <>
            <Button
              disabled={complete.isPending}
              variant='secondary'
              onClick={() => setCompleteTradeNo(null)}
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              variant='primary'
              loading={complete.isPending}
              onClick={() => {
                if (completeTradeNo)
                  complete.mutate(completeTradeNo, {
                    onSuccess: () => setCompleteTradeNo(null),
                  });
              }}
            >
              {t(`${topupNs}.history.complete`)}
            </Button>
          </>
        }
      />
    </>
  );
}

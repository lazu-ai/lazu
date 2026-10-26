import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import dayjs from 'dayjs';
import {
  CalendarRange,
  FileDown,
  FileText,
  Mail,
  TrendingUp,
  Wallet,
  Zap,
} from 'lucide-react';
import React, { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import VChart from '../../components/common/charts/VChart';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import ConsolePage from '../../components/ui/console-page';
import { DataCard } from '../../components/ui/data-card';
import { DataTableShell } from '../../components/ui/data-table-shell';
import { DatePicker } from '../../components/ui/date-picker';
import { EmptyState } from '../../components/ui/empty-state';
import { Switch } from '../../components/ui/switch';
import { CHART_CONFIG } from '../../constants/dashboard.constants';
import { API, asMicroUSD, renderQuota, showSuccess } from '../../helpers';
import { apiErrorHandler, unwrap } from '../../lib/api-error';
import { readBrandToken, useBodyThemeMode } from '../../lib/brand-token';
import { queryKeys } from '../../lib/query-keys';
import type { ColumnDef } from '@tanstack/react-table';
import type { LucideProps } from 'lucide-react';
import type { ComponentType, ReactNode } from 'react';

const ns = 'page.console.monthlyReport';

type SummaryOverview = {
  total_cost?: number;
  total_requests?: number;
  total_tokens?: number;
};

type TopModel = {
  cost?: number;
  name: string;
  requests?: number;
  tokens?: number;
};

type MonthlySummary = {
  overview?: SummaryOverview;
  top_models?: TopModel[];
};

type MonthlyReport = Record<string, unknown> & {
  email_sent?: boolean;
  id?: number | string;
  month?: number;
  pdf_url?: string;
  status?: string;
  summary?: MonthlySummary | string;
  year?: number;
};

type Subscription = {
  enabled: boolean;
  include_pdf: boolean;
  notify_email: boolean;
};

const defaultSubscription: Subscription = {
  enabled: true,
  include_pdf: true,
  notify_email: true,
};

const normalizeSummary = (summary: unknown): MonthlySummary | null => {
  if (!summary) return null;
  if (typeof summary === 'string') {
    try {
      const parsed: unknown = JSON.parse(summary);
      return parsed && typeof parsed === 'object'
        ? (parsed as MonthlySummary)
        : null;
    } catch (error) {
      return null;
    }
  }
  return typeof summary === 'object' ? (summary as MonthlySummary) : null;
};

const buildTrendSeries = (totalCost: unknown, days: number) => {
  const total = Number(totalCost) || 0;
  if (days <= 0) return [];
  if (total <= 0) {
    return Array.from({ length: days }).map((_, index) => ({
      day: index + 1,
      amount: 0,
    }));
  }
  const base = total / days;
  return Array.from({ length: days }).map((_, index) => {
    const amountFactor = 0.6 + (index % 5) * 0.08;
    return {
      day: index + 1,
      amount: Number((base * amountFactor).toFixed(2)),
    };
  });
};

const MetricCard = ({
  icon: Icon,
  label,
  value,
  helper,
  variant = 'neutral',
}: {
  helper: ReactNode;
  icon: ComponentType<LucideProps>;
  label: ReactNode;
  value: ReactNode;
  variant?: 'danger' | 'info' | 'neutral' | 'success' | 'warning';
}) => (
  <DataCard>
    <div className='flex items-start justify-between gap-4'>
      <div>
        <div className='text-xs font-semibold uppercase tracking-[0.08em] text-[var(--ui-text-tertiary)]'>
          {label}
        </div>
        <div className='mt-3 font-mono text-xl font-semibold text-[var(--ui-text)]'>
          {value}
        </div>
        <div className='mt-2 text-sm text-[var(--ui-text-secondary)]'>
          {helper}
        </div>
      </div>
      <Badge variant={variant}>
        <Icon aria-hidden='true' size={14} />
      </Badge>
    </div>
  </DataCard>
);

const MonthlyReportPage = () => {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [selectedMonth, setSelectedMonth] = useState<Date>(
    dayjs().subtract(1, 'month').startOf('month').toDate(),
  );

  const reportsQuery = useQuery<MonthlyReport[]>({
    queryKey: [
      ...queryKeys.console.table('monthly-reports', { page: 1, pageSize: 12 }),
    ],
    queryFn: async () => {
      const page = unwrap<{ items?: unknown } | null>(
        await API.get('/api/reports/monthly', {
          params: { page: 1, page_size: 12 },
        }),
      );
      return Array.isArray(page?.items) ? (page.items as MonthlyReport[]) : [];
    },
  });

  const latestQuery = useQuery<MonthlyReport | null>({
    queryKey: [...queryKeys.console.detail('monthly-report', 'latest')],
    queryFn: async () => {
      // No report yet answers `data: null`.
      return (
        unwrap<MonthlyReport | null>(
          await API.get('/api/reports/monthly/latest'),
        ) || null
      );
    },
  });

  const subscriptionQuery = useQuery<Subscription>({
    queryKey: [...queryKeys.console.detail('monthly-report', 'subscription')],
    queryFn: async () => {
      return (
        unwrap<Subscription | null>(
          await API.get('/api/reports/monthly/subscription'),
        ) || defaultSubscription
      );
    },
  });

  const generateMutation = useMutation({
    mutationFn: async () => {
      return unwrap<MonthlyReport>(
        await API.post('/api/reports/monthly/generate', null, {
          params: {
            year: dayjs(selectedMonth).year(),
            month: dayjs(selectedMonth).month() + 1,
          },
        }),
      );
    },
    onSuccess: () => {
      showSuccess(t(`${ns}.generate.success`));
      queryClient.invalidateQueries({ queryKey: queryKeys.console.root });
    },
    onError: apiErrorHandler,
  });

  const subscriptionMutation = useMutation<Subscription, Error, Subscription>({
    mutationFn: async (next) => {
      unwrap(await API.put('/api/reports/monthly/subscription', next));
      return next;
    },
    onSuccess: () => {
      showSuccess(t(`${ns}.subscription.saved`));
      queryClient.invalidateQueries({ queryKey: queryKeys.console.root });
    },
    onError: apiErrorHandler,
  });

  const latestReport = latestQuery.data;
  const reports = reportsQuery.data || [];
  const subscription = subscriptionQuery.data || defaultSubscription;
  const summary = useMemo(
    () => normalizeSummary(latestReport?.summary),
    [latestReport],
  );
  const overview = summary?.overview || {};
  const topModels = Array.isArray(summary?.top_models)
    ? summary.top_models
    : [];
  const daysInMonth = selectedMonth ? dayjs(selectedMonth).daysInMonth() : 30;
  const trendSeries = useMemo(
    () => buildTrendSeries(overview.total_cost || 0, daysInMonth),
    [daysInMonth, overview.total_cost],
  );

  const bodyTheme = useBodyThemeMode();
  /* Charts pull the brand's teal from the live token so both themes match
     the G1 palette (orange stays banned in data viz). */
  const trendSpec = useMemo(() => {
    const accent = readBrandToken('--lazu-chart-1', '#1f6f6b');
    const grid = readBrandToken('--lazu-data-grid', '#e4e4e7');
    return {
      type: 'line',
      data: [{ id: 'trend', values: trendSeries }],
      xField: 'day',
      yField: 'amount',
      smooth: true,
      area: { visible: true, style: { fill: accent, fillOpacity: 0.2 } },
      line: { style: { lineWidth: 3, stroke: accent } },
      point: { visible: true, size: 3, style: { fill: accent } },
      axes: [
        { orient: 'left', grid: { style: { stroke: grid } } },
        { orient: 'bottom' },
      ],
      tooltip: {
        mark: {
          content: [
            {
              key: t(`${ns}.chart.dailySpend`),
              value: (datum: { amount?: number } | undefined) =>
                renderQuota(asMicroUSD(datum?.amount)),
            },
          ],
        },
      },
      color: [accent],
    };
  }, [bodyTheme, t, trendSeries]);

  const reportColumns = useMemo<ColumnDef<MonthlyReport, unknown>[]>(
    () => [
      {
        header: t(`${ns}.table.month`),
        accessorKey: 'period',
        cell: ({ row }) =>
          `${row.original.year}-${String(row.original.month).padStart(2, '0')}`,
      },
      {
        header: t(`${ns}.table.status`),
        accessorKey: 'status',
        cell: ({ getValue }) => {
          const value = String(getValue() || 'pending');
          const labels: Record<string, string> = {
            completed: t(`${ns}.status.completed`),
            generating: t(`${ns}.status.generating`),
            pending: t(`${ns}.status.pending`),
          };
          const variant =
            value === 'completed'
              ? 'success'
              : value === 'generating'
                ? 'warning'
                : 'neutral';
          return (
            <Badge variant={variant}>{labels[value] || labels.pending}</Badge>
          );
        },
      },
      {
        header: t(`${ns}.table.requests`),
        accessorKey: 'total_requests',
        cell: ({ row }) =>
          normalizeSummary(row.original.summary)?.overview?.total_requests ??
          '-',
      },
      {
        header: t(`${ns}.table.cost`),
        accessorKey: 'total_cost',
        cell: ({ row }) =>
          renderQuota(
            asMicroUSD(
              normalizeSummary(row.original.summary)?.overview?.total_cost,
            ),
          ),
      },
      {
        header: t(`${ns}.table.email`),
        accessorKey: 'email_sent',
        cell: ({ getValue }) => (
          <Badge variant={getValue() ? 'success' : 'neutral'}>
            {getValue() ? t(`${ns}.status.sent`) : t(`${ns}.status.notSent`)}
          </Badge>
        ),
      },
      {
        header: t(`${ns}.table.pdf`),
        accessorKey: 'pdf_url',
        cell: ({ getValue }) =>
          getValue() ? (
            <Button
              onClick={() => window.open(String(getValue()), '_blank')}
              size='sm'
              variant='ghost'
            >
              <FileDown aria-hidden='true' size={14} />
              {t(`${ns}.action.download`)}
            </Button>
          ) : (
            '-'
          ),
      },
    ],
    [t],
  );

  const updateSubscription = (patch: Partial<Subscription>) => {
    subscriptionMutation.mutate({ ...subscription, ...patch });
  };

  const actions = (
    <>
      <DatePicker
        value={selectedMonth}
        onChange={(value) => value && setSelectedMonth(value)}
      />
      <Button
        loading={generateMutation.isPending}
        onClick={() => generateMutation.mutate()}
        variant='primary'
      >
        <FileText aria-hidden='true' size={15} />
        {t(`${ns}.action.generate`)}
      </Button>
      <Button
        disabled={!latestReport?.pdf_url}
        onClick={() =>
          latestReport?.pdf_url && window.open(latestReport.pdf_url, '_blank')
        }
      >
        <FileDown aria-hidden='true' size={15} />
        {t(`${ns}.action.exportPdf`)}
      </Button>
    </>
  );

  return (
    <ConsolePage
      title={t(`${ns}.title`)}
      description={t(`${ns}.description`)}
      badge='REPORT'
      actions={actions}
    >
      {!latestReport ? (
        <DataCard>
          <div className='text-sm text-[var(--ui-text-secondary)]'>
            {t(`${ns}.notice.noReport`)}
          </div>
        </DataCard>
      ) : null}
      <div className='grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4'>
        <MetricCard
          icon={Wallet}
          label={t(`${ns}.metric.cost`)}
          value={renderQuota(asMicroUSD(overview.total_cost))}
          helper={t(`${ns}.metric.monthTotal`)}
          variant='info'
        />
        <MetricCard
          icon={TrendingUp}
          label={t(`${ns}.metric.requests`)}
          value={(overview.total_requests || 0).toLocaleString()}
          helper={t(`${ns}.metric.calls`)}
          variant='success'
        />
        <MetricCard
          icon={Zap}
          label={t(`${ns}.metric.tokens`)}
          value={(overview.total_tokens || 0).toLocaleString()}
          helper={t(`${ns}.metric.tokenTotal`)}
        />
        <MetricCard
          icon={Mail}
          label={t(`${ns}.metric.subscription`)}
          value={
            subscription.enabled
              ? t(`${ns}.status.enabled`)
              : t(`${ns}.status.disabled`)
          }
          helper={t(`${ns}.metric.autoEmail`)}
          variant='warning'
        />
      </div>
      <div className='grid grid-cols-1 gap-4 lg:grid-cols-3'>
        <DataCard
          className='lg:col-span-2'
          title={t(`${ns}.chart.title`)}
          description={t(`${ns}.chart.description`)}
        >
          <div className='h-72'>
            <VChart spec={trendSpec} option={CHART_CONFIG} />
          </div>
        </DataCard>

        <DataCard
          title={t(`${ns}.subscription.title`)}
          description={t(`${ns}.subscription.description`)}
        >
          <div className='grid gap-5 text-sm'>
            {[
              [
                'enabled',
                t(`${ns}.subscription.enable`),
                t(`${ns}.subscription.enableHelp`),
              ],
              [
                'notify_email',
                t(`${ns}.subscription.email`),
                t(`${ns}.subscription.emailHelp`),
              ],
              [
                'include_pdf',
                t(`${ns}.subscription.pdf`),
                t(`${ns}.subscription.pdfHelp`),
              ],
            ].map(([key, label, help]) => (
              <div
                className='flex items-center justify-between gap-4'
                key={key}
              >
                <div>
                  <div className='font-semibold text-[var(--ui-text)]'>
                    {label}
                  </div>
                  <div className='mt-1 text-xs text-[var(--ui-text-secondary)]'>
                    {help}
                  </div>
                </div>
                <Switch
                  checked={Boolean(subscription[key as keyof Subscription])}
                  disabled={key !== 'enabled' && !subscription.enabled}
                  onCheckedChange={(checked) =>
                    updateSubscription({ [key]: checked })
                  }
                />
              </div>
            ))}
          </div>
        </DataCard>
      </div>
      <div className='grid grid-cols-1 gap-4 lg:grid-cols-3'>
        <DataCard
          className='lg:col-span-2'
          title={t(`${ns}.history.title`)}
          description={t(`${ns}.history.description`)}
        >
          <DataTableShell
            columns={reportColumns}
            data={reports.map((item) => ({
              ...item,
              id: item.id || `${item.year}-${item.month}`,
            }))}
            empty={<EmptyState title={t(`${ns}.empty.reports`)} />}
            loading={reportsQuery.isLoading}
          />
        </DataCard>

        <DataCard
          title={t(`${ns}.models.title`)}
          description={t(`${ns}.models.description`)}
        >
          {topModels.length ? (
            <div className='grid gap-4'>
              {topModels.map((model) => (
                <div
                  className='flex items-center justify-between gap-4 text-sm'
                  key={model.name}
                >
                  <div className='min-w-0'>
                    <div className='truncate font-semibold text-[var(--ui-text)]'>
                      {model.name}
                    </div>
                    <div className='mt-1 text-xs text-[var(--ui-text-secondary)]'>
                      {t(`${ns}.models.requests`, {
                        count: model.requests || 0,
                      })}
                    </div>
                  </div>
                  <div className='text-right font-mono'>
                    <div>{renderQuota(asMicroUSD(model.cost))}</div>
                    <div className='text-xs text-[var(--ui-text-secondary)]'>
                      {model.tokens?.toLocaleString() || 0}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <EmptyState title={t(`${ns}.empty.models`)} />
          )}
        </DataCard>
      </div>
    </ConsolePage>
  );
};

export default MonthlyReportPage;

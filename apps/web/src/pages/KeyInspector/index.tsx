import { zodResolver } from '@hookform/resolvers/zod';
import dayjs from 'dayjs';
import {
  Activity,
  BarChart3,
  Coins,
  PieChart,
  Search,
  TrendingUp,
  Zap,
} from 'lucide-react';
import { useMemo, useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import {
  KeyIdentityCard,
  StatCard,
  applyLocalLogFilters,
  buildColorMap,
  createDefaultRange,
  inspectorSchema,
  toRange,
  type ChartDatum,
  type InspectorFormValues,
  type InspectorLogs,
  type InspectorResult,
  type UsageRow,
} from './support';
import VChart from '../../components/common/charts/VChart';
import {
  Button,
  DataSurface,
  DataTableShell,
  DateRangePicker,
  EmptyState,
  Field,
  Input,
  PageHeader,
  Pagination,
  Select,
  SelectItem,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Tag,
} from '../../components/ui';
import { KeyLookupHeading } from '../../components/ui/key-lookup';
import { compactNumber, formatUnixTime } from '../../components/ui/table-tools';
import { CHART_CONFIG } from '../../constants/dashboard.constants';
import { API } from '../../helpers/api';
import { asMicroUSD } from '../../helpers/money';
import { renderQuota } from '../../helpers/render';
import {
  getUserIdFromLocalStorage,
  showError,
  showWarning,
} from '../../helpers/utils';
import { unwrap } from '../../lib/api-error';
import type { CellContext, ColumnDef } from '@tanstack/react-table';

const KeyInspectorPage = () => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<InspectorResult | null>(null);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [logModelFilter, setLogModelFilter] = useState('all');
  const [logKeyword, setLogKeyword] = useState('');
  const [activeTab, setActiveTab] = useState('trend');
  const form = useForm<InspectorFormValues>({
    defaultValues: {
      apiKey: '',
      dateRange: createDefaultRange(),
    },
    resolver: zodResolver(inspectorSchema),
  });
  const isLoggedIn = getUserIdFromLocalStorage() !== -1;

  const requestInspect = async (
    formValues: InspectorFormValues = form.getValues(),
    nextPage = page,
    nextPageSize = pageSize,
    filterOverrides: { logKeyword?: string; logModelName?: string } = {},
  ) => {
    const range = toRange(formValues.dateRange);
    // Date range is optional — when the picker is empty or cleared we
    // fall back to the last 7 days instead of blocking the query.
    const fallbackRange = createDefaultRange();
    const rangeFrom = range.from || fallbackRange.from;
    const rangeTo = range.to || fallbackRange.to;
    const cleanKey = String(formValues.apiKey || '').trim();
    const nextLogModelFilter =
      filterOverrides.logModelName ?? logModelFilter ?? 'all';
    const nextLogKeyword = String(
      filterOverrides.logKeyword ?? logKeyword ?? '',
    ).trim();

    if (!cleanKey) {
      showError(t('legacy.i18n.please_enter_the_key.9a7744ecd8'));
      return;
    }

    setLoading(true);
    try {
      const inspectApiPath =
        getUserIdFromLocalStorage() !== -1
          ? '/api/token/inspect'
          : '/api/public/token/inspect';
      const payload: Record<string, unknown> = {
        api_key: cleanKey,
        end_timestamp: dayjs(rangeTo).endOf('day').unix(),
        log_keyword: nextLogKeyword,
        log_model_name: nextLogModelFilter === 'all' ? '' : nextLogModelFilter,
        model_limit: 50,
        page: nextPage,
        page_size: nextPageSize,
        start_timestamp: dayjs(rangeFrom).startOf('day').unix(),
      };
      const data = unwrap<InspectorResult | null>(
        await API.post(inspectApiPath, payload),
      );

      let nextResult = data || null;
      const hasRequestedFilter = Boolean(
        payload.log_model_name || payload.log_keyword,
      );
      const serverFilters = data?.logs?.filters;
      if (nextResult && hasRequestedFilter && !serverFilters) {
        const filteredItems = applyLocalLogFilters(
          data?.logs?.items || [],
          payload.log_model_name,
          payload.log_keyword,
        );
        nextResult = {
          ...data,
          logs: {
            ...(data?.logs || {}),
            filters: {
              log_keyword: nextLogKeyword,
              log_model_name:
                nextLogModelFilter === 'all' ? '' : nextLogModelFilter,
            },
            items: filteredItems,
            total: filteredItems.length,
          },
        };
        showWarning(
          t(
            'legacy.i18n.your_backend_version_does_not_support_server_side_filters_yet_au.8586f138b1',
          ),
        );
      }

      setResult(nextResult);
      setPage(nextPage);
      setPageSize(nextPageSize);
    } catch (error) {
      showError(error, t('legacy.i18n.query_failed.381ea3ac5e'));
    } finally {
      setLoading(false);
    }
  };

  const tokenMeta = result?.token || {};
  const summary = result?.summary || {};
  const byModel: UsageRow[] = result?.by_model || [];
  const logs: InspectorLogs = result?.logs || {};
  const logItems: UsageRow[] = logs.items || [];
  const hasData =
    result && (Number(summary.request_count || 0) > 0 || logItems.length > 0);
  const remainingPercent =
    tokenMeta.total_available && tokenMeta.total_quota
      ? (tokenMeta.total_available / tokenMeta.total_quota) * 100
      : 0;

  const modelNames = useMemo(
    () => byModel.map((item) => item.model_name || '-'),
    [byModel],
  );
  const colorMap = useMemo(() => buildColorMap(modelNames), [modelNames]);
  const trendData = useMemo(
    () =>
      [...logItems].reverse().map((item, index) => ({
        index: index + 1,
        quota: Number(item.amount_microusd || 0),
        time: dayjs.unix(Number(item.created_at || 0)).format('MM-DD HH:mm'),
        tokens:
          Number(item.prompt_tokens || 0) + Number(item.completion_tokens || 0),
      })),
    [logItems],
  );
  const modelQuotaData = useMemo(
    () =>
      byModel.map((item) => ({
        model: item.model_name || '-',
        quota: Number(item.amount_microusd || 0),
      })),
    [byModel],
  );
  const modelRequestData = useMemo(
    () =>
      byModel.map((item) => ({
        model: item.model_name || '-',
        requests: Number(item.request_count || 0),
      })),
    [byModel],
  );
  const logModelOptions = useMemo(() => {
    const modelSet = new Set<string>();
    byModel.forEach((item) => {
      if (item?.model_name) {
        modelSet.add(item.model_name);
      }
    });
    logItems.forEach((item) => {
      if (item?.model_name) {
        modelSet.add(item.model_name);
      }
    });
    return ['all', ...Array.from(modelSet).sort()];
  }, [byModel, logItems]);

  const trendSpec = useMemo(
    () => ({
      area: {
        style: {
          fill: 'l(270) 0:#3b82f6 1:rgba(59,130,246,0.05)',
          fillOpacity: 0.3,
        },
      },
      data: [{ id: 'trend', values: trendData }],
      legends: { visible: false },
      line: {
        style: {
          lineWidth: 3,
          stroke: '#3b82f6',
        },
      },
      point: { visible: true },
      tooltip: {
        mark: {
          content: [
            {
              key: () => t('legacy.i18n.quota_usage.71f3614c99'),
              value: (datum: ChartDatum) =>
                renderQuota(asMicroUSD(datum.quota)),
            },
            {
              key: () => t('legacy.i18n.token_usage.0ce7002aab'),
              value: (datum: ChartDatum) => compactNumber(datum.tokens || 0),
            },
          ],
        },
      },
      type: 'area',
      xField: 'time',
      yField: 'quota',
    }),
    [t, trendData],
  );

  const modelQuotaSpec = useMemo(
    () => ({
      categoryField: 'model',
      color: { specified: colorMap },
      data: [{ id: 'quotaByModel', values: modelQuotaData }],
      innerRadius: 0.5,
      legends: { visible: false },
      outerRadius: 0.8,
      tooltip: {
        mark: {
          content: [
            {
              key: (datum: ChartDatum) => String(datum.model || ''),
              value: (datum: ChartDatum) =>
                renderQuota(asMicroUSD(datum.quota)),
            },
          ],
        },
      },
      type: 'pie',
      valueField: 'quota',
    }),
    [colorMap, modelQuotaData],
  );

  const modelRequestSpec = useMemo(
    () => ({
      bar: { style: { cornerRadius: [0, 4, 4, 0] } },
      color: { specified: colorMap },
      data: [{ id: 'requestByModel', values: modelRequestData }],
      direction: 'horizontal',
      label: {
        formatter: (datum: ChartDatum) => compactNumber(datum.requests || 0),
        position: 'right',
        visible: true,
      },
      legends: { visible: false },
      seriesField: 'model',
      type: 'bar',
      xField: 'requests',
      yField: 'model',
    }),
    [colorMap, modelRequestData],
  );

  const modelColumns = useMemo<ColumnDef<UsageRow, unknown>[]>(
    () => [
      {
        accessorKey: 'model_name',
        header: t('legacy.i18n.model.98fd0cbd9c'),
        id: 'model',
        cell: ({ getValue }: CellContext<UsageRow, unknown>) => (
          <Tag>{String(getValue() || '-')}</Tag>
        ),
      },
      {
        accessorKey: 'request_count',
        header: t('legacy.i18n.requests.8e71b474d7'),
        id: 'requests',
        cell: ({ getValue }: CellContext<UsageRow, unknown>) =>
          compactNumber(getValue() || 0),
      },
      {
        accessorKey: 'amount_microusd',
        header: t('legacy.i18n.quota_usage.71f3614c99'),
        id: 'quota',
        cell: ({ getValue }: CellContext<UsageRow, unknown>) =>
          renderQuota(asMicroUSD(getValue())),
      },
      {
        accessorKey: 'prompt_tokens',
        header: t('page.keyusage.column.promptTokens'),
        id: 'prompt',
        cell: ({ getValue }: CellContext<UsageRow, unknown>) =>
          compactNumber(getValue() || 0),
      },
      {
        accessorKey: 'completion_tokens',
        header: t('page.keyusage.column.completionTokens'),
        id: 'completion',
        cell: ({ getValue }: CellContext<UsageRow, unknown>) =>
          compactNumber(getValue() || 0),
      },
      {
        accessorKey: 'last_used_at',
        header: t('legacy.i18n.recent_calls.2cea0cbc55'),
        id: 'lastUsed',
        cell: ({ getValue }: CellContext<UsageRow, unknown>) =>
          formatUnixTime(getValue()),
      },
    ],
    [t],
  );

  const logColumns = useMemo<ColumnDef<UsageRow, unknown>[]>(
    () => [
      {
        accessorKey: 'created_at',
        header: t('legacy.i18n.time.89b4aa6364'),
        id: 'createdAt',
        cell: ({ getValue }: CellContext<UsageRow, unknown>) =>
          formatUnixTime(getValue()),
      },
      {
        accessorKey: 'model_name',
        header: t('legacy.i18n.model.98fd0cbd9c'),
        id: 'model',
        cell: ({ getValue }: CellContext<UsageRow, unknown>) => (
          <Tag>{String(getValue() || '-')}</Tag>
        ),
      },
      {
        accessorKey: 'amount_microusd',
        header: t('legacy.i18n.quota.3fa9a9d38e'),
        id: 'quota',
        cell: ({ getValue }: CellContext<UsageRow, unknown>) =>
          renderQuota(asMicroUSD(getValue())),
      },
      {
        accessorKey: 'prompt_tokens',
        header: t('page.keyusage.column.prompt'),
        id: 'prompt',
        cell: ({ getValue }: CellContext<UsageRow, unknown>) =>
          compactNumber(getValue() || 0),
      },
      {
        accessorKey: 'completion_tokens',
        header: t('page.keyusage.column.completion'),
        id: 'completion',
        cell: ({ getValue }: CellContext<UsageRow, unknown>) =>
          compactNumber(getValue() || 0),
      },
      {
        accessorKey: 'use_time',
        header: t('legacy.i18n.duration.a9704e1997'),
        id: 'duration',
        cell: ({ getValue }: CellContext<UsageRow, unknown>) =>
          `${Number(getValue() || 0)}ms`,
      },
      {
        accessorKey: 'content',
        header: t('legacy.i18n.usage_description.18c06eea64'),
        id: 'content',
        cell: ({ getValue }: CellContext<UsageRow, unknown>) => (
          <span
            className='block max-w-[28rem] truncate text-xs text-[var(--ui-text-secondary)]'
            title={String(getValue() || '-')}
          >
            {String(getValue() || '-')}
          </span>
        ),
      },
    ],
    [t],
  );

  const applyLogFilters = () => {
    requestInspect(form.getValues(), 1, logs.page_size || pageSize);
  };

  const resetLogFilters = () => {
    setLogModelFilter('all');
    setLogKeyword('');
    requestInspect(form.getValues(), 1, logs.page_size || pageSize, {
      logKeyword: '',
      logModelName: 'all',
    });
  };

  const keyField = (
    <Field>
      <label className='lazu-label' htmlFor='inspect-key'>
        {t('legacy.i18n.api_key.5df1d83e18')}
      </label>
      <Input
        id='inspect-key'
        placeholder={t('legacy.i18n.enter_api_key.99a3ab7c44')}
        type='password'
        {...form.register('apiKey')}
      />
    </Field>
  );

  const dateField = (
    <Field>
      <label className='lazu-label'>
        {t('legacy.i18n.date_range.3783f29c30')}
      </label>
      <Controller
        control={form.control}
        name='dateRange'
        render={({ field }) => (
          <DateRangePicker
            value={toRange(field.value)}
            onChange={field.onChange}
          />
        )}
      />
    </Field>
  );

  return (
    <section className='console-page-shell'>
      {result && isLoggedIn ? (
        <PageHeader
          title={t('legacy.i18n.key_usage_inspector.b39e7197a8')}
          description={t(
            'legacy.i18n.query_quota_usage_and_call_details_directly_via_api_key.37694a613c',
          )}
        />
      ) : null}
      <div className='console-page-body'>
        {!result ? (
          <div className='key-lookup'>
            {/* No card and no icon in a rounded box: the page is the eyebrow,
                the title, the sentence and one row of fields on the paper. */}
            <KeyLookupHeading />
            <form
              className='key-lookup__form'
              onSubmit={form.handleSubmit((values) =>
                requestInspect(values, 1, pageSize),
              )}
            >
              {keyField}
              {dateField}
              <Button
                size='lg'
                variant='primary'
                loading={loading}
                type='submit'
              >
                {t('legacy.i18n.query.711363c424')}
              </Button>
            </form>
          </div>
        ) : (
          <>
            <DataSurface>
              <form
                className='grid gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(16rem,22rem)_auto]'
                onSubmit={form.handleSubmit((values) =>
                  requestInspect(values, 1, pageSize),
                )}
              >
                {keyField}
                {dateField}
                <div className='flex items-end'>
                  <Button fullWidth loading={loading} type='submit'>
                    <Search size={15} />
                    {t('legacy.i18n.query.711363c424')}
                  </Button>
                </div>
              </form>
            </DataSurface>

            <KeyIdentityCard token={tokenMeta} t={t} />

            <div className='grid gap-3 md:grid-cols-2 xl:grid-cols-4'>
              <StatCard
                icon={Coins}
                label={t('legacy.i18n.remaining_quota.531e3faa2b')}
                value={renderQuota(asMicroUSD(tokenMeta.total_available))}
                progress={
                  tokenMeta.unlimited_quota ? undefined : remainingPercent
                }
                subtitle={
                  tokenMeta.unlimited_quota
                    ? t('legacy.i18n.unlimited_quota.b26f37b7f5')
                    : `${remainingPercent.toFixed(1)}% ${t('legacy.i18n.remaining.f37c034a97')}`
                }
                variant='info'
              />
              <StatCard
                icon={Zap}
                label={t('legacy.i18n.quota_used.c5d7bd13e2')}
                value={renderQuota(asMicroUSD(tokenMeta.total_used))}
                subtitle={t('legacy.i18n.total_usage.075a99430b')}
                variant='warning'
              />
              <StatCard
                icon={Activity}
                label={t('legacy.i18n.requests.8e71b474d7')}
                value={compactNumber(summary.request_count || 0)}
                subtitle={t('legacy.i18n.api_calls.5a96a2f373')}
                variant='success'
              />
              <StatCard
                icon={BarChart3}
                label={t('legacy.i18n.token_usage.0ce7002aab')}
                value={compactNumber(summary.total_tokens || 0)}
                subtitle={t('Prompt + Completion')}
                variant='neutral'
              />
            </div>

            {hasData ? (
              <DataSurface title={t('page.keyusage.analyticsTitle')}>
                <Tabs value={activeTab} onValueChange={setActiveTab}>
                  <TabsList>
                    <TabsTrigger value='trend'>
                      <TrendingUp size={15} />
                      {t('legacy.i18n.consumption_trend.08eb71f895')}
                    </TabsTrigger>
                    <TabsTrigger value='distribution'>
                      <PieChart size={15} />
                      {t('legacy.i18n.consumption_distribution.d663c368f7')}
                    </TabsTrigger>
                    <TabsTrigger value='ranking'>
                      <BarChart3 size={15} />
                      {t('legacy.i18n.usage_ranking.e8dbbcbe2c')}
                    </TabsTrigger>
                  </TabsList>
                  <TabsContent value='trend'>
                    {trendData.length ? (
                      <div className='h-[320px]'>
                        <VChart spec={trendSpec} option={CHART_CONFIG} />
                      </div>
                    ) : (
                      <EmptyState title={t('legacy.i18n.no_data.b246458f20')} />
                    )}
                  </TabsContent>
                  <TabsContent value='distribution'>
                    {modelQuotaData.length ? (
                      <div className='h-[320px]'>
                        <VChart spec={modelQuotaSpec} option={CHART_CONFIG} />
                      </div>
                    ) : (
                      <EmptyState title={t('legacy.i18n.no_data.b246458f20')} />
                    )}
                  </TabsContent>
                  <TabsContent value='ranking'>
                    {modelRequestData.length ? (
                      <div className='h-[320px]'>
                        <VChart spec={modelRequestSpec} option={CHART_CONFIG} />
                      </div>
                    ) : (
                      <EmptyState title={t('legacy.i18n.no_data.b246458f20')} />
                    )}
                  </TabsContent>
                </Tabs>
              </DataSurface>
            ) : null}

            {hasData && byModel.length ? (
              <DataSurface
                title={t('legacy.i18n.usage_details_by_model.ba665ff036')}
              >
                <DataTableShell
                  data={byModel}
                  columns={modelColumns}
                  loading={loading}
                  rowKey='model_name'
                />
              </DataSurface>
            ) : null}

            {hasData ? (
              <DataSurface
                title={t('legacy.i18n.request_details.2b673a6692')}
                description={t('page.keyusage.logDescription')}
              >
                <div className='mb-3 flex flex-col gap-2 lg:flex-row lg:items-end'>
                  <Field className='min-w-[14rem]'>
                    <label className='lazu-label'>
                      {t('legacy.i18n.filter_by_model.573da6162e')}
                    </label>
                    <Select
                      value={logModelFilter}
                      onValueChange={setLogModelFilter}
                    >
                      {logModelOptions.map((model) => (
                        <SelectItem key={model} value={model}>
                          {model === 'all'
                            ? t('page.console.tables.all')
                            : model}
                        </SelectItem>
                      ))}
                    </Select>
                  </Field>
                  <Field className='min-w-[18rem] flex-1'>
                    <label
                      className='lazu-label'
                      htmlFor='keyusage-log-keyword'
                    >
                      {t(
                        'legacy.i18n.keyword_model_request_id_usage_description.91148d716a',
                      )}
                    </label>
                    <Input
                      id='keyusage-log-keyword'
                      value={logKeyword}
                      onChange={(event) => setLogKeyword(event.target.value)}
                      placeholder={t(
                        'legacy.i18n.keyword_model_request_id_usage_description.91148d716a',
                      )}
                    />
                  </Field>
                  <div className='flex gap-2'>
                    <Button variant='ghost' onClick={resetLogFilters}>
                      {t('legacy.i18n.reset.3d81345303')}
                    </Button>
                    <Button loading={loading} onClick={applyLogFilters}>
                      <Search size={15} />
                      {t('legacy.i18n.filter.dcce9a144a')}
                    </Button>
                  </div>
                </div>
                {logItems.length ? (
                  <>
                    <DataTableShell
                      data={logItems}
                      columns={logColumns}
                      loading={loading}
                      rowKey={(record) =>
                        record.request_id || `${record.created_at}-${record.id}`
                      }
                    />
                    <Pagination
                      className='mt-4'
                      currentPage={logs.page || page}
                      pageSize={logs.page_size || pageSize}
                      total={logs.total || 0}
                      showSizeChanger
                      pageSizeOpts={[10, 20, 50, 100]}
                      onPageChange={(nextPage) =>
                        requestInspect(
                          form.getValues(),
                          nextPage,
                          logs.page_size || pageSize,
                        )
                      }
                      onPageSizeChange={(nextSize) =>
                        requestInspect(form.getValues(), 1, nextSize)
                      }
                    />
                  </>
                ) : (
                  <EmptyState title={t('legacy.i18n.no_data.b246458f20')} />
                )}
              </DataSurface>
            ) : null}
          </>
        )}
      </div>
    </section>
  );
};

export default KeyInspectorPage;

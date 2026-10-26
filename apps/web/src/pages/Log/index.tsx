import { Activity, ChevronDown, Download, X } from 'lucide-react';
import React, {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import {
  BudgetGuardPanel,
  UsageUserRanking,
  type LogViewMode,
} from './log-insights';
import {
  LogDetailPanel,
  NUMERIC_LOG_COLUMNS,
  UsageLogFilters as UsageLogFiltersPanel,
  downloadUsageLogs,
  readBlobDownloadError,
  requestIdentity,
  renderLogCell,
  shortTokens,
  type LogCellContext,
  type LogRecord,
  type UsageRequestRecord,
} from './log-support';
import { SK } from './sessions/session-format';
import SessionsView from './sessions/SessionsView';
import {
  Button,
  DataTableShell,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  EmptyState,
  Modal,
  Pagination,
  Pending,
  PendingValue,
} from '../../components/ui';
import {
  ColumnVisibilityMenu,
  ConsoleTableFrame,
  DEFAULT_PAGE_SIZE_OPTIONS,
  InlineCode,
  MetricPill,
  TextStack,
  compactNumber,
  useColumnVisibility,
} from '../../components/ui/table-tools';
import { useConsoleScope } from '../../context/ConsoleScope';
import { rejectionLabelKey } from '../../features/projects/rejections';
import { asMicroUSD } from '../../helpers/money';
import { renderQuota } from '../../helpers/render';
import { showError } from '../../helpers/utils';
import {
  LOG_COLUMN_KEYS,
  useSpendBudgetsQuery,
  useUsageLogRequestQuery,
  useUsageLogsQuery,
  useUsageUserAggregateQuery,
} from '../../hooks/usage-logs/useUsageLogsQuery';
import { UsageHeadSlotContext } from '../Usage/headSlot';
import './log-table.css';
import type { ConsoleColumnMeta } from '../../components/ui/table-tools';
import type { UsageProjectScope } from '../../hooks/usage-logs/usageProjectScope';
import type { UsageUserAggregateRow } from '../../hooks/usage-logs/useUsageLogsQuery';
import type { ColumnDef } from '@tanstack/react-table';

/* How a log row is identified for selection. requestIdentity is the public
   request id and can be empty; the table already keys rows by `key`, so that is
   the reliable handle and the request id is only the fallback. */
const rowIdentity = (record: LogRecord) =>
  String((record as { key?: string | number }).key ?? requestIdentity(record));

type ScopeParam = 'result' | 'reason' | 'token_id' | 'user_id';

/* A deep link from a rejection chip or a Key row filters this table by
   project, result, reason and Key. Say so, and let each filter be dropped —
   silently filtered rows read as "the project barely gets any traffic". */
function ProjectScopeChips({
  onClear,
  projectName,
  scope,
  tokenName,
  userName,
}: {
  onClear: (param: ScopeParam | 'all') => void;
  projectName: string;
  scope?: UsageProjectScope;
  tokenName?: string;
  userName?: string;
}) {
  /* The reason labels live in the `projects` namespace, not this page's. */
  const { t } = useTranslation(['projects', 'translation'], {
    nsMode: 'fallback',
  });
  if (!scope?.project_id) return null;
  const chips: { key: ScopeParam; label: string }[] = [];
  if (scope.result === 'rejected')
    chips.push({ key: 'result', label: t('page.console.log.scope.rejected') });
  if (scope.reason)
    chips.push({
      key: 'reason',
      label: t('page.console.log.scope.reason', {
        reason: t(rejectionLabelKey(scope.reason)),
      }),
    });
  if (scope.user_id)
    chips.push({
      key: 'user_id',
      label: t('page.console.log.scope.member', {
        name: userName || scope.user_id,
      }),
    });
  if (scope.token_id)
    chips.push({
      key: 'token_id',
      label: t('page.console.log.scope.key', {
        name: tokenName || scope.token_id,
      }),
    });
  if (!chips.length) return null;
  return (
    <div className='logv2-scopechips'>
      <span className='logv2-scopechips__lb'>
        {t('page.console.log.scope.filtering')}
      </span>
      <span className='pj-rsn' data-tone='mute' data-compact>
        {t('page.console.log.scope.project', { name: projectName })}
      </span>
      {chips.map((chip) => (
        <button
          className='logv2-scopechip'
          key={chip.key}
          onClick={() => onClear(chip.key)}
          type='button'
        >
          {chip.label}
          <X aria-hidden='true' size={12} />
        </button>
      ))}
      {chips.length > 1 ? (
        <button
          className='pj-linkbtn'
          onClick={() => onClear('all')}
          type='button'
        >
          {t('page.console.log.scope.clearAll')}
        </button>
      ) : null}
    </div>
  );
}

const Token = () => {
  const logs = useUsageLogsQuery();
  const { t } = logs;
  const { availableScopes, setActiveScope } = useConsoleScope();
  const headSlot = useContext(UsageHeadSlotContext);
  const platformScope = availableScopes.find(
    (scope) => scope.plane === 'platform',
  );
  const customerScope = availableScopes.find(
    (scope) => scope.plane === 'customer',
  );
  const [searchParams, setSearchParams] = useSearchParams();
  const selectLogScope = (scopeId: string) => {
    setActiveScope(scopeId);
    const next = new URLSearchParams(searchParams);
    next.delete('scope');
    if (next.has('project_id')) {
      for (const key of [
        'project_id',
        'result',
        'reason',
        'token_id',
        'user_id',
      ])
        next.delete(key);
    }
    setSearchParams(next, { replace: true });
  };
  const clearScopeParam = useCallback(
    (param: ScopeParam | 'all') => {
      const next = new URLSearchParams(searchParams);
      if (param === 'all')
        for (const key of ['result', 'reason', 'token_id', 'user_id'])
          next.delete(key);
      else next.delete(param);
      /* A reason without a result reads as a filter that is not applied. */
      if (param === 'result') next.delete('reason');
      setSearchParams(next, { replace: true });
    },
    [searchParams, setSearchParams],
  );
  const [viewMode, setViewMode] = useState<LogViewMode>('requests');
  const [selectedRequest, setSelectedRequest] = useState<LogRecord | null>(
    null,
  );
  const [requestRevealVersion, setRequestRevealVersion] = useState(0);
  const revealedRef = useRef(-1);
  const selectRequest = useCallback(
    (record: LogRecord) => {
      const alreadyOpen =
        selectedRequest !== null &&
        rowIdentity(selectedRequest) === rowIdentity(record);
      setSelectedRequest(alreadyOpen ? null : record);
      setRequestRevealVersion((current) => current + 1);
      const id = requestIdentity(record);
      if (id) {
        const next = new URLSearchParams(searchParams);
        next.set('request', id);
        setSearchParams(next, { replace: true });
      }
    },
    [searchParams, selectedRequest, setSearchParams],
  );
  const selectedRequestId = selectedRequest
    ? requestIdentity(selectedRequest)
    : '';
  /* A row expands in place — but a deep link can name a request that is not on
     this page at all, fetched on its own. That one has no row to sit under, so
     it renders as a standalone panel above the list instead of silently not
     appearing. */
  const selectionOnPage = Boolean(
    selectedRequestId &&
    logs.usageLogs.some(
      (record) => requestIdentity(record) === selectedRequestId,
    ),
  );

  const requested = searchParams.get('request') || '';
  const requestedFromPage = useMemo(
    () =>
      logs.usageLogs.find((record) => requestIdentity(record) === requested) as
        | UsageRequestRecord
        | undefined,
    [logs.usageLogs, requested],
  );
  const requestedQuery = useUsageLogRequestQuery({
    admin: logs.isAdminUser,
    enabled: Boolean(requested && !requestedFromPage),
    requestId: requested,
  });

  useEffect(() => {
    if (!requested) return;
    const match = requestedFromPage || requestedQuery.data;
    if (match) {
      setSelectedRequest(match as LogRecord);
    } else if (!requestedQuery.isFetching) {
      setSelectedRequest(null);
    }
  }, [
    requested,
    requestedFromPage,
    requestedQuery.data,
    requestedQuery.isFetching,
  ]);

  useEffect(() => {
    if (!logs.isAdminUser && viewMode === 'budgets') {
      setViewMode('requests');
    }
  }, [logs.isAdminUser, viewMode]);

  // 请求 | 会话 are the page's two views; 明细 / 用户 is a body switch inside
  // the request view: same filters, same window, same stat tiles. Both live
  // in the URL so refresh and back keep them.
  const showingSessions = searchParams.get('view') === 'sessions';
  const setSessionsView = useCallback(
    (next: boolean) => {
      const params = new URLSearchParams(searchParams);
      if (next) {
        params.set('view', 'sessions');
        params.delete('request');
      } else {
        params.delete('view');
      }
      setSearchParams(params);
    },
    [searchParams, setSearchParams],
  );
  const usersRequested = searchParams.get('view') === 'users';
  const showingUsers =
    usersRequested && viewMode === 'requests' && logs.isAdminUser;
  const setUsersView = useCallback(
    (next: boolean) => {
      const params = new URLSearchParams(searchParams);
      if (next) {
        params.set('view', 'users');
      } else {
        params.delete('view');
      }
      setSearchParams(params, { replace: true });
    },
    [searchParams, setSearchParams],
  );
  useEffect(() => {
    if (usersRequested && !logs.isAdminUser) {
      setUsersView(false);
    }
  }, [logs.isAdminUser, setUsersView, usersRequested]);

  const userAggregateQuery = useUsageUserAggregateQuery({
    admin: logs.isAdminUser,
    enabled: showingUsers,
    filters: logs.filters,
  });
  const userRows = useMemo(
    () =>
      [...(userAggregateQuery.data || [])].sort(
        (a, b) =>
          Number(b.amount_microusd || 0) - Number(a.amount_microusd || 0),
      ),
    [userAggregateQuery.data],
  );
  const userRowsTotalMicrousd = userRows.reduce(
    (sum, row) => sum + Number(row.amount_microusd || 0),
    0,
  );
  const userRowsPeakMicrousd = userRows.reduce(
    (peak, row) => Math.max(peak, Number(row.amount_microusd || 0)),
    0,
  );
  const drillIntoUser = useCallback(
    (row: UsageUserAggregateRow) => {
      logs.submitFilters({
        ...logs.filters,
        user_id: String(row.id || ''),
        username: String(row.name || ''),
      });
      setUsersView(false);
    },
    [logs, setUsersView],
  );

  const showingBudgets = viewMode === 'budgets';
  const budgetQuery = useSpendBudgetsQuery(logs.isAdminUser && showingBudgets);

  /* Name the member and the Key in the chips from the rows already loaded;
     fall back to the opaque id rather than fetching just for a label. */
  const scopeUserName = useMemo(
    () =>
      logs.usageLogs.find(
        (row: LogRecord) => String(row.user_id ?? '') === logs.project?.user_id,
      )?.username,
    [logs.project?.user_id, logs.usageLogs],
  );
  const scopeTokenName = useMemo(
    () =>
      logs.usageLogs.find(
        (row: LogRecord) => Boolean(logs.project?.token_id) && row.token_name,
      )?.token_name,
    [logs.project?.token_id, logs.usageLogs],
  );

  const columnMeta = useMemo<ConsoleColumnMeta[]>(() => {
    const adminOnly = (meta: ConsoleColumnMeta[]) =>
      logs.isAdminUser ? meta : [];
    return [
      { id: LOG_COLUMN_KEYS.TIME, label: t('page.console.log.column.time') },
      {
        id: LOG_COLUMN_KEYS.SESSION,
        label: t(`${SK}.list.col.sessionColumn`, { defaultValue: '会话' }),
      },
      // Attribution is the first thing an operator reads on a platform-wide
      // table, so the user column sits next to the timestamp and ships on.
      ...adminOnly([
        {
          id: LOG_COLUMN_KEYS.USERNAME,
          label: t('page.console.log.column.user'),
        },
      ]),
      { id: LOG_COLUMN_KEYS.TYPE, label: t('page.console.log.column.status') },
      // Inside a shared project a rejected row has to say WHY it was rejected;
      // the platform-wide table keeps its original columns.
      ...(logs.project?.project_id
        ? [
            {
              id: LOG_COLUMN_KEYS.REASON,
              label: t('page.console.log.column.reason'),
            },
          ]
        : []),
      // The lane (稳定 / Agent) is part of "which model answered", so it rides
      // in the model cell instead of a hidden 分组 column.
      {
        id: LOG_COLUMN_KEYS.MODEL,
        label: t('page.console.log.column.modelLane'),
      },
      { id: LOG_COLUMN_KEYS.TOKEN, label: t('page.console.log.column.key') },
      { id: LOG_COLUMN_KEYS.PROMPT, label: t('page.console.log.column.io') },
      {
        id: LOG_COLUMN_KEYS.USE_TIME,
        label: t('page.console.log.column.latencyFirst'),
      },
      ...adminOnly([
        {
          id: LOG_COLUMN_KEYS.CHANNEL,
          label: t('page.console.log.column.channel'),
        },
        {
          id: LOG_COLUMN_KEYS.RETRY,
          label: t('page.console.log.column.retry'),
        },
      ]),
      { id: LOG_COLUMN_KEYS.COST, label: t('page.console.log.column.cost') },
      {
        id: LOG_COLUMN_KEYS.IP,
        label: t('page.console.log.column.ip'),
        defaultHidden: true,
      },
    ];
  }, [logs.isAdminUser, logs.project?.project_id, t]);
  const columnVisibility = useColumnVisibility(
    logs.isAdminUser
      ? 'page-console-log-columns-admin-v7'
      : 'page-console-log-columns-user-v7',
    columnMeta,
  );
  const cellContext = useMemo<LogCellContext>(
    () => ({
      copyText: logs.copyText,
      isAdminUser: logs.isAdminUser,
      isOpen: (record: LogRecord) =>
        Boolean(selectedRequestId) &&
        requestIdentity(record) === selectedRequestId,
      onOpen: selectRequest,
      showUserInfo: logs.showUserInfo,
      t,
    }),
    [logs, selectRequest, selectedRequestId, t],
  );
  const mobileRequestColumns = useMemo<ColumnDef<LogRecord, unknown>[]>(
    () => [
      ...columnMeta.map(
        (column): ColumnDef<LogRecord, unknown> => ({
          id: column.id,
          header: String(column.label || column.id),
          cell: ({ row }) =>
            renderLogCell(column.id, row.original, cellContext),
        }),
      ),
      {
        id: 'receipt',
        header: t('page.console.log.detail.open', { defaultValue: '详情' }),
        cell: ({ row }: { row: { original: LogRecord } }) => (
          <Button
            size='xs'
            variant='ghost'
            onClick={(event) => {
              event.stopPropagation();
              selectRequest(row.original);
            }}
          >
            {t('page.console.log.detail.open', { defaultValue: '详情' })}
          </Button>
        ),
      },
    ],
    [cellContext, columnMeta, selectRequest, t],
  );

  const exportLogs = async (format: 'csv' | 'xlsx') => {
    try {
      await downloadUsageLogs(logs.filters, format, logs.isAdminUser);
    } catch (error) {
      showError(
        await readBlobDownloadError(error),
        t('page.console.log.action.exportFailed'),
      );
    }
  };

  /* The operator's scope switch rides in the 筛选 popover; when it is set to
     仅我自己 a chip says so above the table and drops back to 平台全局. */
  const scopeSwitch =
    platformScope && customerScope ? (
      <span className='logv2-seg'>
        <button
          type='button'
          aria-pressed={logs.isAdminUser}
          className='logv2-seg__button'
          data-active={logs.isAdminUser ? 'true' : undefined}
          onClick={() => selectLogScope(platformScope.id)}
        >
          {t('page.console.log.scope.platform', {
            defaultValue: '平台全局',
          })}
        </button>
        <button
          type='button'
          aria-pressed={!logs.isAdminUser}
          className='logv2-seg__button'
          data-active={!logs.isAdminUser ? 'true' : undefined}
          onClick={() => selectLogScope(customerScope.id)}
        >
          {t('page.console.log.scope.self', {
            defaultValue: '仅我自己',
          })}
        </button>
      </span>
    ) : null;
  const scopeChip =
    platformScope && customerScope && !logs.isAdminUser ? (
      <span className='logv2-filter-chip'>
        {t('page.console.log.filter.chipScope', {
          defaultValue: '范围：{{value}}',
          value: t('page.console.log.scope.self', { defaultValue: '仅我自己' }),
        })}
        <button
          type='button'
          aria-label={t('page.console.log.scope.platform', {
            defaultValue: '平台全局',
          })}
          onClick={() => selectLogScope(platformScope.id)}
        >
          <X aria-hidden='true' size={12} />
        </button>
      </span>
    ) : null;

  /* 列 and 导出 belong to the page head, beside the title. */
  const headTools =
    !showingBudgets && headSlot
      ? createPortal(
          <>
            {!showingUsers ? (
              <ColumnVisibilityMenu
                t={t}
                columns={columnMeta}
                value={columnVisibility.value}
                onChange={columnVisibility.setColumnVisible}
              />
            ) : null}
            <DropdownMenu>
              <DropdownMenuTrigger>
                <Button size='sm' variant='secondary'>
                  <Download size={15} />
                  {t('page.console.log.action.export', {
                    defaultValue: '匯出',
                  })}
                  <ChevronDown aria-hidden='true' size={13} />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align='end'>
                <DropdownMenuItem onSelect={() => exportLogs('csv')}>
                  {t('page.console.log.action.exportCsv')}
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => exportLogs('xlsx')}>
                  {t('page.console.log.action.exportXlsx')}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </>,
          headSlot,
        )
      : null;

  if (showingSessions) {
    return <SessionsView admin={logs.isAdminUser} t={t} />;
  }

  return (
    <>
      {headTools}
      <ConsoleTableFrame
        showRefresh={false}
        className='usage-log-page'
        filtersAsForm={false}
        t={t}
        title={null}
        compact
        isFetching={
          showingBudgets
            ? budgetQuery.isFetching
            : showingUsers
              ? userAggregateQuery.isFetching
              : logs.isFetching
        }
        onRefresh={
          showingBudgets
            ? budgetQuery.refresh
            : showingUsers
              ? () => userAggregateQuery.refetch()
              : logs.refresh
        }
        stats={
          showingBudgets ? (
            <div className='flex flex-wrap gap-2'>
              <MetricPill
                label={t('page.console.log.budget.metric.rules')}
                value={compactNumber(budgetQuery.budgets.length)}
                variant='info'
              />
              <MetricPill
                label={t('page.console.log.budget.metric.alerts')}
                value={compactNumber(budgetQuery.alerts.length)}
                variant='warning'
              />
              <MetricPill
                label={t('page.console.log.budget.metric.blocks')}
                value={compactNumber(
                  budgetQuery.alerts.filter(
                    (alert) => alert.alert_type === 'blocked',
                  ).length,
                )}
                variant='danger'
              />
            </div>
          ) : (
            /* Every figure reads off a query that may not have answered yet;
               `logs.total` defaults to 0 while in flight, and a strip reading
               "请求 0" over a table that is still loading contradicts itself. */
            <div className='logv2-summary'>
              <div className='logv2-summary__cell'>
                <span>{t('page.console.log.summary.spend')}</span>
                <b>
                  <Pending chars={6} pending={logs.isLoading}>
                    {`$${(Number(logs.stat.amount_microusd || 0) / 1_000_000).toFixed(2)}`}
                  </Pending>
                </b>
              </div>
              <div className='logv2-summary__cell'>
                <span>{t('page.console.log.summary.requests')}</span>
                <b>
                  <Pending chars={4} pending={logs.isLoading}>
                    {logs.totalAtLeast ? (
                      <span
                        title={t(
                          'page.console.log.summary.requestsAtLeastHint',
                          {
                            total: logs.total.toLocaleString(),
                          },
                        )}
                      >
                        {t('page.console.log.summary.requestsAtLeast', {
                          total: logs.total.toLocaleString(),
                        })}
                      </span>
                    ) : (
                      logs.total.toLocaleString()
                    )}
                  </Pending>
                </b>
              </div>
              <div className='logv2-summary__cell'>
                <span>RPM</span>
                <b>
                  <Pending chars={3} pending={logs.isLoading}>
                    {Number(logs.stat.rpm || 0).toLocaleString()}
                  </Pending>
                </b>
              </div>
              <div className='logv2-summary__cell'>
                <span>TPM</span>
                <b>
                  <Pending chars={4} pending={logs.isLoading}>
                    {shortTokens(logs.stat.tpm)}
                  </Pending>
                </b>
              </div>
            </div>
          )
        }
        filters={
          showingBudgets ? null : (
            <>
              <ProjectScopeChips
                onClear={clearScopeParam}
                projectName={
                  availableScopes.find(
                    (scope) => scope.project?.id === logs.project?.project_id,
                  )?.label ??
                  logs.project?.project_id ??
                  ''
                }
                scope={logs.project}
                tokenName={scopeTokenName}
                userName={scopeUserName}
              />
              <UsageLogFiltersPanel
                logs={logs}
                scope={scopeSwitch}
                scopeChip={scopeChip}
                viewSwitcher={
                  logs.isAdminUser && viewMode === 'requests' ? (
                    <span className='logv2-seg'>
                      <button
                        type='button'
                        aria-pressed={!showingUsers}
                        className='logv2-seg__button'
                        data-active={showingUsers ? undefined : 'true'}
                        onClick={() => setUsersView(false)}
                      >
                        {t('page.console.log.view.detail', {
                          defaultValue: '明细',
                        })}
                      </button>
                      <button
                        type='button'
                        aria-pressed={showingUsers}
                        className='logv2-seg__button'
                        data-active={showingUsers ? 'true' : undefined}
                        onClick={() => setUsersView(true)}
                      >
                        {t('page.console.log.view.users', {
                          defaultValue: '用户',
                        })}
                      </button>
                    </span>
                  ) : null
                }
              />
            </>
          )
        }
      >
        {showingBudgets ? (
          <BudgetGuardPanel query={budgetQuery} t={t} />
        ) : showingUsers ? (
          <UsageUserRanking
            loading={userAggregateQuery.isLoading}
            onDrill={drillIntoUser}
            peakMicrousd={userRowsPeakMicrousd}
            rows={userRows}
            t={t}
            totalMicrousd={userRowsTotalMicrousd}
          />
        ) : (
          <div className='usage-investigation'>
            <div className='usage-investigation__master'>
              {selectedRequest && !selectionOnPage ? (
                <div
                  aria-label='Selected request receipt'
                  className='logv2-card logv2-receipt logv2-receipt--offpage'
                  ref={(node) => {
                    if (node && requestRevealVersion !== revealedRef.current) {
                      revealedRef.current = requestRevealVersion;
                      node.focus({ preventScroll: true });
                    }
                  }}
                  tabIndex={-1}
                >
                  <LogDetailPanel
                    record={selectedRequest}
                    standalone
                    t={t}
                    copyText={logs.copyText}
                    isAdminUser={logs.isAdminUser}
                  />
                </div>
              ) : null}
              <div className='logv2-card logv2-desktop-master'>
                {/* While the page is in flight the table holds a page's
                    worth of height, so the pager under it holds still. Once
                    the answer is here the table is as tall as the answer:
                    a page with three rows is three rows, not ten. */}
                <div
                  className='logv2-scroll'
                  style={
                    {
                      '--logv2-page-rows': logs.isLoading
                        ? Math.min(logs.pageSize || 10, 10)
                        : Math.max(logs.usageLogs.length, 2),
                    } as React.CSSProperties
                  }
                >
                  <table className='logv2-table'>
                    <thead>
                      <tr>
                        {columnVisibility.visibleColumns.map((column) => (
                          <th
                            key={column.id}
                            className={
                              NUMERIC_LOG_COLUMNS.has(column.id)
                                ? 'logv2-num'
                                : undefined
                            }
                          >
                            {column.label}
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {logs.isLoading ? (
                        /* One "加载中…" row, and everything under the table —
                           the pager, the investigation panel — sat a few
                           dozen pixels from the top and was thrown down when
                           the rows arrived (measured: 0.17 layout shift, the
                           worst in the console). The outline is the page the
                           query is about to return, at its height. */
                        Array.from({
                          length: Math.min(logs.pageSize || 10, 10),
                        }).map((_unused, rowIndex) => (
                          <tr aria-hidden='true' key={`pending-${rowIndex}`}>
                            {columnVisibility.visibleColumns.map((column) => (
                              <td
                                className={
                                  NUMERIC_LOG_COLUMNS.has(column.id)
                                    ? 'logv2-num'
                                    : undefined
                                }
                                key={column.id}
                              >
                                <PendingValue chars={6} />
                              </td>
                            ))}
                          </tr>
                        ))
                      ) : logs.usageLogs.length === 0 ? (
                        <tr>
                          <td colSpan={columnVisibility.visibleColumns.length}>
                            <EmptyState
                              icon={<Activity size={24} />}
                              title={t(
                                'legacy.i18n.no_results_found.3f5746269e',
                              )}
                              description={t(
                                'page.console.log.emptyDescription',
                              )}
                            />
                          </td>
                        </tr>
                      ) : (
                        logs.usageLogs.flatMap((record) => {
                          const isOpen = Boolean(
                            selectedRequestId &&
                            requestIdentity(record) === selectedRequestId,
                          );
                          return [
                            <tr
                              key={record.key}
                              className='logv2-row'
                              data-selected={isOpen ? 'true' : undefined}
                              onClick={() => selectRequest(record)}
                            >
                              {columnVisibility.visibleColumns.map((column) => (
                                <td
                                  key={column.id}
                                  className={
                                    NUMERIC_LOG_COLUMNS.has(column.id)
                                      ? 'logv2-num'
                                      : undefined
                                  }
                                >
                                  {renderLogCell(
                                    column.id,
                                    record,
                                    cellContext,
                                  )}
                                </td>
                              ))}
                            </tr>,
                            isOpen ? (
                              <tr
                                className='logv2-detail-row'
                                key={`${record.key}-detail`}
                              >
                                <td
                                  colSpan={
                                    columnVisibility.visibleColumns.length
                                  }
                                >
                                  {/* Focus moves here on open so the detail is
                                      announced and keyboard-reachable, but with
                                      preventScroll: the reader keeps their
                                      place in the list. */}
                                  <div
                                    aria-label='Selected request receipt'
                                    className='logv2-receipt'
                                    ref={(node) => {
                                      if (
                                        node &&
                                        requestRevealVersion !==
                                          revealedRef.current
                                      ) {
                                        revealedRef.current =
                                          requestRevealVersion;
                                        node.focus({ preventScroll: true });
                                      }
                                    }}
                                    tabIndex={-1}
                                  >
                                    <LogDetailPanel
                                      record={record}
                                      t={t}
                                      copyText={logs.copyText}
                                      isAdminUser={logs.isAdminUser}
                                    />
                                  </div>
                                </td>
                              </tr>
                            ) : null,
                          ];
                        })
                      )}
                    </tbody>
                  </table>
                </div>
                <div className='logv2-foot'>
                  <span>
                    {logs.isLoading ? (
                      <PendingValue chars={18} />
                    ) : (
                      t(
                        logs.totalAtLeast
                          ? 'page.console.log.footer.summaryAtLeast'
                          : 'page.console.log.footer.summary',
                        {
                          cost: renderQuota(
                            asMicroUSD(logs.stat.amount_microusd),
                            6,
                          ),
                          defaultValue: '共 {{total}} 条 · 消费 {{cost}}',
                          total: compactNumber(logs.total),
                        },
                      )
                    )}
                  </span>
                  <Pagination
                    currentPage={logs.page}
                    pageSize={logs.pageSize}
                    total={logs.total}
                    showTotal={false}
                    showSizeChanger
                    pageSizeOpts={DEFAULT_PAGE_SIZE_OPTIONS}
                    onPageChange={logs.setPage}
                    onPageSizeChange={logs.setPageSize}
                  />
                </div>
              </div>
              <div className='logv2-mobile-master'>
                {/* The card list gets the same disclosure as the table: the
                    detail belongs to one row and opens under it, on either
                    layout. */}
                <DataTableShell
                  columns={mobileRequestColumns}
                  data={logs.usageLogs}
                  empty={
                    <EmptyState
                      icon={<Activity size={24} />}
                      title={t('legacy.i18n.no_results_found.3f5746269e')}
                      description={t('page.console.log.emptyDescription')}
                    />
                  }
                  loading={logs.isLoading}
                  mobileColumns={mobileRequestColumns}
                  onRowClick={selectRequest}
                  renderExpanded={(record: LogRecord) => (
                    <div className='logv2-receipt'>
                      <LogDetailPanel
                        record={record}
                        t={t}
                        copyText={logs.copyText}
                        isAdminUser={logs.isAdminUser}
                      />
                    </div>
                  )}
                  rowKey='key'
                  selectedRowKey={selectedRequest?.key ?? null}
                />
                <div className='logv2-foot logv2-mobile-foot'>
                  <span>
                    {logs.isLoading ? (
                      <PendingValue chars={18} />
                    ) : (
                      t(
                        logs.totalAtLeast
                          ? 'page.console.log.footer.summaryAtLeast'
                          : 'page.console.log.footer.summary',
                        {
                          cost: renderQuota(
                            asMicroUSD(logs.stat.amount_microusd),
                            6,
                          ),
                          defaultValue: '共 {{total}} 条 · 消费 {{cost}}',
                          total: compactNumber(logs.total),
                        },
                      )
                    )}
                  </span>
                  <Pagination
                    currentPage={logs.page}
                    pageSize={logs.pageSize}
                    total={logs.total}
                    showTotal={false}
                    onPageChange={logs.setPage}
                  />
                </div>
              </div>
            </div>
          </div>
        )}
      </ConsoleTableFrame>
      <Modal
        open={Boolean(logs.userInfo)}
        onOpenChange={(open) => {
          if (!open) {
            logs.setUserInfo(null);
          }
        }}
        title={t('legacy.i18n.user_information.55c26aba77')}
        body={
          logs.userInfo ? (
            <div className='grid gap-2 text-sm text-[var(--ui-text)]'>
              <TextStack
                title={logs.userInfo.username || logs.userInfo.display_name}
                description={logs.userInfo.email}
              />
              <div className='grid grid-cols-2 gap-2'>
                <InlineCode>
                  {t('page.console.tables.idValue', {
                    id: logs.userInfo.id,
                  })}
                </InlineCode>
                <InlineCode>{logs.userInfo.group || '-'}</InlineCode>
              </div>
            </div>
          ) : null
        }
      />
    </>
  );
};

export default Token;

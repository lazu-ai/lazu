import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { getUsageLogOwnerScope } from './usageLogOwnerScope';
import { getUsageLogUrlRange } from './usageLogUrlRange';
import {
  getUsageProjectScope,
  type UsageProjectScope,
} from './usageProjectScope';
import { useUsageScope } from './useUsageScope';
import { trimFilterValues } from '../../components/ui/filter-bar';
import {
  coerceItemsPayload,
  createSearchParams,
} from '../../components/ui/table-tools';
import { ITEMS_PER_PAGE } from '../../constants/common.constant';
import { API } from '../../helpers/api';
import { getLogOther } from '../../helpers/log';
import { asMicroUSD } from '../../helpers/money';
import { renderQuota } from '../../helpers/render';
import {
  copy,
  getTodayStartTimestamp,
  showError,
  showSuccess,
  timestamp2string,
} from '../../helpers/utils';
import { apiErrorHandler, unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';

type DateRangeValue = Date | number | string | undefined;

type UsageLogDateRange =
  | [DateRangeValue, DateRangeValue]
  | {
      exact?: boolean;
      from?: DateRangeValue;
      to?: DateRangeValue;
    };

export type UsageLogFilters = {
  channel: string;
  dateRange: UsageLogDateRange;
  group: string;
  logType: string;
  model_name: string;
  token_name: string;
  user_id: string;
  username: string;
};

type UsageLogRecord = {
  agent_id?: string;
  amount_microusd?: number;
  channel?: number | string;
  channel_id?: number;
  channel_name?: string;
  content?: string;
  created_at?: number;
  group?: string;
  id?: number | string;
  ip?: string;
  is_stream?: boolean;
  line_items?: unknown;
  model_name?: string;
  other?: unknown;
  prompt_tokens?: number;
  completion_tokens?: number;
  /** Contract rejection code on a rejected request; empty on a normal one. */
  reason?: string;
  request_id?: string;
  retry?: unknown;
  /** Session the request belongs to; see useAgentSessions. */
  session_id?: string;
  /** Where session_id came from; "token" = grouped by Key, no id sent. */
  session_source?: string;
  thread_id?: string;
  parent_thread_id?: string;
  /** main / subagent / compact / review. */
  request_kind?: string;
  client_framework?: string;
  client_version?: string;
  token_name?: string;
  type?: number;
  use_time?: number;
  user_id?: number | string;
  username?: string;
  [key: string]: unknown;
};

type UsageLogOther = Record<string, unknown> & {
  admin_info?: Record<string, unknown>;
  frt?: number | string;
  ttft_ms?: number;
  group?: string;
  refund?: boolean | string;
  refund_amount?: number;
  refund_display?: string;
  refund_task_id?: string;
  task_id?: string;
  taskId?: string;
  TaskID?: string;
  taskID?: string;
  upstream_model_name?: string;
};

type NormalizedUsageLogRecord = UsageLogRecord & {
  isRefund: boolean;
  key?: number | string;
  otherParsed: UsageLogOther;
  refundCostDisplay: string;
  refundTaskId: string;
  taskIdentifier: string;
  timestamp2string: string;
};

type ItemsPayload<T> = {
  items: T[];
  meta?: unknown;
  page: number;
  pageSize: number;
  total: number;
};

type FetchUsageLogsParams = {
  admin: boolean;
  filters: UsageLogFilters;
  page: number;
  pageSize: number;
  project?: UsageProjectScope;
};

type FetchUsageLogStatParams = {
  admin: boolean;
  filters: UsageLogFilters;
  project?: UsageProjectScope;
};

type FetchUsageLogRequestParams = {
  admin: boolean;
  requestId: string;
};

type FetchUsageUserAggregateParams = {
  admin: boolean;
  filters: UsageLogFilters;
};

/** One row of `by_user` from GET /api/data/dashboard (admin scope only). */
export type UsageUserAggregateRow = {
  amount_microusd?: number;
  completion_tokens?: number;
  error_count?: number;
  id?: number;
  last_used_at?: number;
  name?: string;
  prompt_tokens?: number;
  request_count?: number;
};

type UsageLogStat = Record<string, unknown>;
type UserInfo =
  | (Record<string, unknown> & {
      display_name?: string;
      email?: string;
      group?: string;
      id?: number | string;
      username?: string;
    })
  | null;

export type SpendBudgetScope = 'org' | 'token' | 'user';
export type SpendBudgetPeriod = 'day' | 'month';
export type SpendBudgetEnforcement = 'alert' | 'block';

export type SpendBudget = {
  burst_multiplier?: number;
  created_at?: string;
  enabled?: boolean;
  enforcement?: SpendBudgetEnforcement;
  id: number;
  limit_microusd?: number;
  period?: SpendBudgetPeriod;
  scope?: SpendBudgetScope;
  token_id?: number;
  updated_at?: string;
  user_id?: number;
  warning_threshold_pct?: number;
};

export type SpendBudgetDraft = {
  burst_multiplier: number;
  enabled: boolean;
  enforcement: SpendBudgetEnforcement;
  id?: number;
  limit_microusd: number;
  period: SpendBudgetPeriod;
  scope: SpendBudgetScope;
  token_id: number;
  user_id: number;
  warning_threshold_pct: number;
};

export type SpendBudgetAlert = {
  agent_id?: string;
  alert_type?: string;
  budget_id?: number;
  created_at?: string;
  delta_microusd?: number;
  id: number;
  limit_microusd?: number;
  message?: string;
  period?: string;
  request_id?: string;
  scope?: string;
  session_id?: string;
  spent_microusd?: number;
  threshold_pct?: number;
  token_id?: number;
  user_id?: number;
};

const extractRefundTaskIdFromContent = (content: unknown) => {
  if (!content || typeof content !== 'string') {
    return '';
  }
  let match = content.match(/failed\s+([^\s(，,]+)/i);
  if (match?.[1]) {
    return match[1];
  }
  match = content.match(/任务(?:\s|ID)?([^\s,，]+)/);
  return match?.[1] || '';
};

const extractRefundAmountDisplay = (content: unknown) => {
  if (!content || typeof content !== 'string') {
    return '';
  }
  const match =
    content.match(
      /refund[^\d￥¥＄$¤-]*([￥¥＄$¤]?\s*-?\s*[\d.]+)(?:\s*额度)?/i,
    ) ||
    content.match(/退还[^\d￥¥＄$¤-]*([￥¥＄$¤]?\s*-?\s*[\d.]+)/) ||
    content.match(/退款[^\d￥¥＄$¤-]*([￥¥＄$¤]?\s*-?\s*[\d.]+)/);
  if (!match?.[1]) {
    return '';
  }
  const value = match[1].replace(/\s+/g, '').replace(/额度$/i, '');
  return value.startsWith('-') ? value : `-${value}`;
};

const getTaskAmount = (
  taskAmountMap: Map<string, number | undefined>,
  taskId: unknown,
) => {
  if (!taskId) {
    return undefined;
  }
  const amount = taskAmountMap.get(String(taskId));
  return typeof amount === 'number' ? amount : undefined;
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

const parseUsageLogOther = (value: unknown): UsageLogOther => {
  if (typeof value === 'string' || value === undefined) {
    return getLogOther(value) as UsageLogOther;
  }
  if (isRecord(value)) {
    return value as UsageLogOther;
  }
  return {};
};

const otherString = (other: UsageLogOther, key: keyof UsageLogOther) => {
  const value = other[key];
  return typeof value === 'string' ? value : '';
};

const firstOtherString = (
  other: UsageLogOther,
  keys: Array<keyof UsageLogOther>,
) => {
  for (const key of keys) {
    const value = otherString(other, key);
    if (value) {
      return value;
    }
  }
  return '';
};

const otherNumber = (other: UsageLogOther, key: keyof UsageLogOther) => {
  const value = other[key];
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === 'string') {
    const numberValue = Number(value);
    return Number.isFinite(numberValue) ? numberValue : undefined;
  }
  return undefined;
};

const toDate = (value: DateRangeValue) => {
  if (value instanceof Date) {
    return value;
  }
  if (value === undefined) {
    return new Date();
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? new Date() : date;
};

const toUnixRange = (dateRange: UsageLogDateRange) => {
  const from = Array.isArray(dateRange) ? dateRange[0] : dateRange?.from;
  const to = Array.isArray(dateRange) ? dateRange[1] : dateRange?.to;
  const start = toDate(from);
  const end = toDate(to);
  if (Array.isArray(dateRange) || !dateRange?.exact) {
    start.setHours(0, 0, 0, 0);
    end.setHours(23, 59, 59, 999);
  }
  return {
    end: Math.ceil(end.getTime() / 1000),
    start: Math.floor(start.getTime() / 1000),
  };
};

export const LOG_COLUMN_KEYS = {
  CHANNEL: 'channel',
  COMPLETION: 'completion',
  COST: 'cost',
  DETAILS: 'details',
  GROUP: 'group',
  IP: 'ip',
  MODEL: 'model',
  PROMPT: 'prompt',
  REASON: 'reason',
  RETRY: 'retry',
  SESSION: 'session',
  TIME: 'time',
  TOKEN: 'token',
  TYPE: 'type',
  USE_TIME: 'use_time',
  USERNAME: 'username',
} as const;

export function getDefaultUsageLogFilters(): UsageLogFilters {
  const start = new Date(getTodayStartTimestamp() * 1000);
  const end = new Date();
  end.setHours(end.getHours() + 1);
  return {
    channel: '',
    dateRange: { from: start, to: end },
    group: '',
    logType: '0',
    model_name: '',
    token_name: '',
    user_id: '',
    username: '',
  };
}

export function getInitialUsageLogFilters(
  search = window.location.search,
): UsageLogFilters {
  const filters = getDefaultUsageLogFilters();
  const scope = getUsageLogOwnerScope(search);
  filters.username = scope.username;
  filters.user_id = scope.userID;
  filters.token_name = scope.key;
  filters.model_name = scope.model;
  if (scope.logType) filters.logType = scope.logType;
  const urlRange = getUsageLogUrlRange(search);
  if (urlRange) filters.dateRange = urlRange;
  return filters;
}

const normalizeLog = (record: UsageLogRecord): NormalizedUsageLogRecord => {
  const other = parseUsageLogOther(record?.other);
  const content = record?.content || '';
  const taskIdentifier = firstOtherString(other, [
    'task_id',
    'taskId',
    'TaskID',
    'taskID',
  ]);
  const refundAmount = otherNumber(other, 'refund_amount');
  const refundDisplay = otherString(other, 'refund_display');
  let isRefund =
    record?.type === 6 ||
    other?.refund === true ||
    other?.refund === 'true' ||
    (typeof record?.amount_microusd === 'number' && record.amount_microusd < 0);
  if (
    !isRefund &&
    (content.toLowerCase().includes('refund') ||
      content.includes(String.fromCharCode(0x9000, 0x6b3e)))
  ) {
    isRefund = true;
  }

  return {
    ...record,
    isRefund,
    key: record?.id,
    otherParsed: other,
    refundCostDisplay:
      refundDisplay ||
      (typeof refundAmount === 'number' ? `-${Math.abs(refundAmount)}` : '') ||
      extractRefundAmountDisplay(content),
    refundTaskId:
      otherString(other, 'refund_task_id') ||
      extractRefundTaskIdFromContent(content),
    taskIdentifier,
    timestamp2string: timestamp2string(record?.created_at || 0),
  };
};

const normalizeLogs = (
  records: UsageLogRecord[],
): NormalizedUsageLogRecord[] => {
  const taskAmountMap = new Map<string, number | undefined>();
  records.forEach((record) => {
    const other = parseUsageLogOther(record?.other);
    const taskIdentifier = firstOtherString(other, [
      'task_id',
      'taskId',
      'TaskID',
      'taskID',
    ]);
    if (
      taskIdentifier &&
      typeof record?.type === 'number' &&
      [0, 2, 5].includes(record.type)
    ) {
      taskAmountMap.set(String(taskIdentifier), record?.amount_microusd);
    }
  });

  return records.map((record) => {
    const normalized = normalizeLog(record);
    if (!normalized.isRefund) {
      return normalized;
    }

    const taskAmount =
      getTaskAmount(taskAmountMap, normalized.taskIdentifier) ??
      getTaskAmount(taskAmountMap, normalized.refundTaskId);
    let refundCostDisplay = '';
    const refundAmount = otherNumber(normalized.otherParsed, 'refund_amount');
    const refundDisplay = otherString(normalized.otherParsed, 'refund_display');
    if (typeof refundAmount === 'number') {
      refundCostDisplay = String(
        renderQuota(asMicroUSD(-Math.abs(refundAmount)), 6),
      );
    } else if (
      typeof normalized.amount_microusd === 'number' &&
      normalized.amount_microusd < 0
    ) {
      refundCostDisplay = String(
        renderQuota(asMicroUSD(normalized.amount_microusd), 6),
      );
    } else if (typeof taskAmount === 'number') {
      refundCostDisplay = String(
        renderQuota(asMicroUSD(-Math.abs(taskAmount)), 6),
      );
    } else if (refundDisplay) {
      refundCostDisplay = refundDisplay;
    }

    return {
      ...normalized,
      refundCostDisplay:
        refundCostDisplay ||
        normalized.refundCostDisplay ||
        extractRefundAmountDisplay(normalized.content),
    };
  });
};

async function fetchUsageLogs({
  admin,
  filters,
  page,
  pageSize,
  project,
}: FetchUsageLogsParams): Promise<ItemsPayload<NormalizedUsageLogRecord>> {
  const range = toUnixRange(filters.dateRange);
  const base = admin ? '/api/log/' : '/api/log/self/';
  const query = createSearchParams({
    ...(project ?? {}),
    channel: admin ? filters.channel : undefined,
    end_timestamp: range.end,
    group: filters.group,
    model_name: filters.model_name,
    p: page,
    page_size: pageSize,
    start_timestamp: range.start,
    token_name: filters.token_name,
    type: filters.logType || '0',
    // A project deep link carries the actor in the URL; the filter form is
    // empty then, so the link's user_id must not be overwritten by it.
    user_id:
      admin || project?.project_id
        ? filters.user_id || project?.user_id
        : undefined,
    username: admin ? filters.username : undefined,
  });
  const pageData = coerceItemsPayload(
    unwrap<unknown>(await API.get(`${base}?${query}`)),
    page,
    pageSize,
  ) as ItemsPayload<UsageLogRecord>;
  return {
    ...pageData,
    items: normalizeLogs(pageData.items),
  };
}

async function fetchUsageLogStat({
  admin,
  filters,
  project,
}: FetchUsageLogStatParams): Promise<UsageLogStat> {
  const range = toUnixRange(filters.dateRange);
  const base = admin ? '/api/log/stat' : '/api/log/self/stat';
  const query = createSearchParams({
    ...(project ?? {}),
    channel: admin ? filters.channel : undefined,
    end_timestamp: range.end,
    group: filters.group,
    model_name: filters.model_name,
    start_timestamp: range.start,
    token_name: filters.token_name,
    type: filters.logType || '0',
    // A project deep link carries the actor in the URL; the filter form is
    // empty then, so the link's user_id must not be overwritten by it.
    user_id:
      admin || project?.project_id
        ? filters.user_id || project?.user_id
        : undefined,
    username: admin ? filters.username : undefined,
  });
  return unwrap<UsageLogStat | null>(await API.get(`${base}?${query}`)) || {};
}

async function fetchUsageLogRequest({
  admin,
  requestId,
}: FetchUsageLogRequestParams): Promise<NormalizedUsageLogRecord | null> {
  const base = admin ? '/api/log/search' : '/api/log/self/search';
  const query = createSearchParams({ keyword: requestId });
  const found = unwrap<unknown>(await API.get(`${base}?${query}`));
  const records = Array.isArray(found) ? (found as UsageLogRecord[]) : [];
  const match = records.find(
    (record) => String(record.request_id || '') === requestId,
  );
  return match ? normalizeLogs([match])[0] : null;
}

/**
 * The dashboard aggregate is role-scoped server side: `scope=self` downgrades
 * the caller to their own rows, anything else keeps the session role (see
 * resolveDashboardScopeRole). Both the 用户 aggregate and the CSV/XLSX export
 * send this so they always match the table the operator is looking at.
 */
export const usageDashboardScopeParam = (admin: boolean) =>
  admin ? 'platform' : 'self';

async function fetchUsageUserAggregate({
  admin,
  filters,
}: FetchUsageUserAggregateParams): Promise<UsageUserAggregateRow[]> {
  const range = toUnixRange(filters.dateRange);
  // A whole-day range ends at the next midnight; ending a second earlier
  // makes it whole hours, which the server answers from hourly rows.
  const end = range.end % 3600 === 0 ? range.end - 1 : range.end;
  const response = await API.get('/api/data/dashboard', {
    params: {
      channel: admin ? filters.channel : undefined,
      end_timestamp: end,
      group: filters.group,
      scope: usageDashboardScopeParam(admin),
      start_timestamp: range.start,
      user_id: admin ? filters.user_id : undefined,
      username: admin ? filters.username : undefined,
      // Only the user rows: the platform dashboard's other parts read every
      // routing outcome of the range.
      view: admin ? 'users' : undefined,
    },
  });
  const rows = unwrap<{ by_user?: unknown } | null>(response)?.by_user;
  return Array.isArray(rows) ? (rows as UsageUserAggregateRow[]) : [];
}

export function useUsageUserAggregateQuery(
  params: FetchUsageUserAggregateParams & { enabled?: boolean },
) {
  const { enabled = true, ...queryParams } = params;
  return useQuery({
    enabled,
    queryKey: queryKeys.console.table('usageLogUsers', queryParams),
    queryFn: () => fetchUsageUserAggregate(queryParams),
    placeholderData: keepPreviousData,
  });
}

async function fetchSpendBudgets(): Promise<SpendBudget[]> {
  const budgets = unwrap<unknown>(await API.get('/api/admin/spend-budgets'));
  return Array.isArray(budgets) ? (budgets as SpendBudget[]) : [];
}

async function fetchSpendBudgetAlerts(): Promise<SpendBudgetAlert[]> {
  const alerts = unwrap<unknown>(
    await API.get('/api/admin/spend-budgets/alerts'),
  );
  return Array.isArray(alerts) ? (alerts as SpendBudgetAlert[]) : [];
}

async function saveSpendBudget(draft: SpendBudgetDraft): Promise<SpendBudget> {
  const id = Number(draft.id || 0);
  const response = id
    ? await API.put(`/api/admin/spend-budgets/${id}`, draft)
    : await API.post('/api/admin/spend-budgets', draft);
  return unwrap<SpendBudget>(response);
}

async function deleteSpendBudget(id: number): Promise<boolean> {
  unwrap(await API.delete(`/api/admin/spend-budgets/${id}`));
  return true;
}

export function useUsageLogRequestQuery(
  params: FetchUsageLogRequestParams & { enabled?: boolean },
) {
  const { enabled = true, ...queryParams } = params;
  return useQuery({
    enabled: enabled && Boolean(queryParams.requestId),
    queryKey: queryKeys.console.detail('usageLogRequest', queryParams),
    queryFn: () => fetchUsageLogRequest(queryParams),
  });
}

export function useSpendBudgetsQuery(enabled: boolean) {
  const queryClient = useQueryClient();
  const budgetsKey = queryKeys.console.table('spendBudgets');
  const alertsKey = queryKeys.console.table('spendBudgetAlerts');
  const budgetsQuery = useQuery({
    enabled,
    queryKey: budgetsKey,
    queryFn: fetchSpendBudgets,
  });
  const alertsQuery = useQuery({
    enabled,
    queryKey: alertsKey,
    queryFn: fetchSpendBudgetAlerts,
  });
  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: budgetsKey });
    queryClient.invalidateQueries({ queryKey: alertsKey });
  };
  const saveMutation = useMutation({
    mutationFn: saveSpendBudget,
    onSuccess: invalidate,
    onError: apiErrorHandler,
  });
  const deleteMutation = useMutation({
    mutationFn: deleteSpendBudget,
    onSuccess: invalidate,
    onError: apiErrorHandler,
  });
  return {
    alerts: alertsQuery.data || [],
    budgets: budgetsQuery.data || [],
    deleteBudget: (id: number) => deleteMutation.mutate(id),
    deletePending: deleteMutation.isPending,
    isFetching: budgetsQuery.isFetching || alertsQuery.isFetching,
    isLoading: budgetsQuery.isLoading || alertsQuery.isLoading,
    refresh: () => {
      budgetsQuery.refetch();
      alertsQuery.refetch();
    },
    saveBudget: (draft: SpendBudgetDraft) => saveMutation.mutate(draft),
    savePending: saveMutation.isPending,
  };
}

export function useUsageLogsQuery() {
  const { t } = useTranslation();
  const { activeScope, admin } = useUsageScope();
  const [searchParams] = useSearchParams();
  const scopeParams = getUsageLogOwnerScope(searchParams.toString());
  const ownerParam = scopeParams.username;
  const userIDParam = scopeParams.userID;
  const keyParam = scopeParams.key;
  const modelParam = scopeParams.model;
  const logTypeParam = scopeParams.logType;
  const [filters, setFilters] = useState<UsageLogFilters>(() =>
    getInitialUsageLogFilters(searchParams.toString()),
  );
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(() => {
    return Number(localStorage.getItem('page-size')) || ITEMS_PER_PAGE;
  });
  const [userInfo, setUserInfo] = useState<UserInfo>(null);

  useEffect(() => {
    setFilters((current) => {
      // A deep link only asserts the dimensions it names; an absent `type`
      // leaves whatever the reader had selected rather than resetting it.
      const nextLogType = logTypeParam || current.logType;
      if (
        current.username === ownerParam &&
        current.user_id === userIDParam &&
        current.token_name === keyParam &&
        current.model_name === modelParam &&
        current.logType === nextLogType
      ) {
        return current;
      }
      return {
        ...current,
        logType: nextLogType,
        model_name: modelParam,
        token_name: keyParam,
        user_id: userIDParam,
        username: ownerParam,
      };
    });
    setPage(1);
  }, [keyParam, logTypeParam, modelParam, ownerParam, userIDParam]);

  // Project scope rides on the console scope (a shared project) and on
  // deep links; the key carries it so switching projects never shows another
  // project's rows through keepPreviousData.
  const activeProjectId =
    !admin && activeScope.plane === 'customer' && activeScope.project?.isShared
      ? activeScope.project.id
      : undefined;
  const project = useMemo(
    () => getUsageProjectScope(searchParams.toString(), activeProjectId),
    [activeProjectId, searchParams],
  );
  const params = useMemo(
    () => ({ admin, filters, page, pageSize, project }),
    [admin, filters, page, pageSize, project],
  );

  const logsQuery = useQuery({
    queryKey: queryKeys.console.table('usageLogs', params),
    queryFn: () => fetchUsageLogs(params),
    placeholderData: project.project_id ? undefined : keepPreviousData,
    // A video or image job's row is written when it is accepted and
    // completed when it finishes; while one is in progress, keep looking.
    refetchInterval: (query) =>
      query.state.data?.items.some(
        (item) => Number(item.type) === 2 && item.otherParsed?.pending === true,
      )
        ? 10_000
        : false,
  });

  const statQuery = useQuery({
    queryKey: queryKeys.console.detail('usageLogs', [
      'stat',
      admin,
      filters,
      project,
    ]),
    queryFn: () => fetchUsageLogStat({ admin, filters, project }),
    placeholderData: project.project_id ? undefined : keepPreviousData,
  });

  const userInfoMutation = useMutation<
    UserInfo,
    Error,
    number | string | null | undefined
  >({
    mutationFn: async (userId) => {
      if (!admin || !userId) {
        return null;
      }
      return unwrap<UserInfo>(await API.get(`/api/user/${userId}`));
    },
    onSuccess: setUserInfo,
    onError: apiErrorHandler,
  });

  const copyText = async (value: unknown) => {
    if (await copy(String(value ?? ''))) {
      showSuccess(t('page.console.tables.copied'));
    } else {
      showError(t('page.console.tables.copyFailed'));
    }
  };

  const submitFilters = (nextFilters: UsageLogFilters) => {
    setFilters(trimFilterValues(nextFilters));
    setPage(1);
  };

  const resetFilters = () => {
    setFilters(getDefaultUsageLogFilters());
    setPage(1);
  };

  const pageData = logsQuery.data || {
    items: [],
    page,
    pageSize,
    total: 0,
  };

  return {
    copyText,
    filters,
    isAdminUser: admin,
    isFetching: logsQuery.isFetching || statQuery.isFetching,
    isLoading: logsQuery.isLoading,
    page: pageData.page || page,
    pageSize: pageData.pageSize || pageSize,
    project,
    refresh: () => {
      logsQuery.refetch();
      statQuery.refetch();
    },
    resetFilters,
    setPage,
    setPageSize: (nextSize: number) => {
      localStorage.setItem('page-size', String(nextSize));
      setPageSize(nextSize);
      setPage(1);
    },
    setUserInfo,
    showUserInfo: (userId: number | string | null | undefined) =>
      userInfoMutation.mutate(userId),
    stat: statQuery.data || {},
    submitFilters,
    t,
    total: pageData.total,
    userInfo,
    userInfoLoading: userInfoMutation.isPending,
    usageLogs: pageData.items,
  };
}

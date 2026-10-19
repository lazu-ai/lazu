/* Sessions: the tasks an agent ran through the gateway, one row per session
 * id per account (internal/model/agent_session.go). Customers read their own
 * through /api/log/self/sessions; operators read everyone's, or one
 * account's with user_id, through /api/log/sessions. */
import {
  keepPreviousData,
  useInfiniteQuery,
  useQuery,
} from '@tanstack/react-query';
import { createSearchParams } from '../../components/ui/table-tools';
import { API } from '../../helpers/api';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';

export type AgentSessionRange = '24h' | '7d' | '30d' | '90d';

export type AgentSessionModel = {
  amount_microusd: number;
  /** The charged part of the requests that had a fair list price. */
  compared_microusd: number;
  eco_requests: number;
  fallback_requests: number;
  model_name: string;
  /** Those same requests at the vendor's list price. */
  official_microusd: number;
  request_count: number;
  stable_requests: number;
  tokens: number;
};

export type AgentSession = {
  amount_microusd: number;
  cached_tokens: number;
  client: string;
  client_version: string;
  compact_count: number;
  compared_microusd: number;
  completion_tokens: number;
  error_count: number;
  /** Public id, the one the detail route takes. */
  id: string;
  last_at: number;
  /** When the latest failed request was logged; >= last_at means the
   *  session's last request failed. */
  last_error_at: number;
  official_microusd: number;
  prompt_tokens: number;
  request_count: number;
  review_count: number;
  /** The id the agent sent (or the per-Key fallback when source is token). */
  session_id: string;
  /** Where session_id came from; "token" = grouped by Key, no id sent. */
  source: string;
  started_at: number;
  subagent_count: number;
  subagent_requests: number;
  /** Opening of the first message. */
  title: string;
  token_name: string;
};

export type AgentSessionListItem = AgentSession & {
  models: AgentSessionModel[];
  tokens: number;
  /** Operator reads only. */
  user_id?: number;
  username?: string;
};

export type AgentSessionListSummary = {
  amount_microusd: number;
  cached_tokens: number;
  compared_microusd: number;
  completion_tokens: number;
  failed_sessions: number;
  median_amount_microusd: number | null;
  median_duration_seconds: number | null;
  median_requests: number | null;
  official_microusd: number;
  prompt_tokens: number;
  request_count: number;
  sessions: number;
  subagents: number;
  tokens: number;
};

export type AgentSessionList = {
  items: AgentSessionListItem[];
  page: number;
  pageSize: number;
  summary: AgentSessionListSummary;
  total: number;
};

export type AgentSessionThread = {
  amount_microusd: number;
  compact_count: number;
  completion_tokens: number;
  error_count: number;
  kind: string;
  last_at: number;
  parent_thread_id: string;
  prompt_tokens: number;
  request_count: number;
  started_at: number;
  /** Empty for the main conversation. */
  thread_id: string;
};

export type AgentSessionRequest = {
  amount_microusd: number;
  cached_tokens: number;
  completion_tokens: number;
  created_at: number;
  duration_seconds: number;
  error?: string;
  failed: boolean;
  kind: string;
  /** stable / eco / fallback; absent when the request reached no lane. */
  lane?: 'stable' | 'eco' | 'fallback';
  model_name: string;
  /** Fair list-price comparison; absent when there is none. */
  official_microusd?: number;
  prompt_tokens: number;
  request_id: string;
  thread_id: string;
};

export type AgentSessionDetail = {
  first_message: string;
  models: AgentSessionModel[];
  requests: AgentSessionRequest[];
  requests_retained: boolean;
  requests_available: number;
  requests_complete: boolean;
  next_request_cursor?: string;
  session: AgentSession;
  threads: AgentSessionThread[];
  username?: string;
};

export type AgentSessionListParams = {
  admin: boolean;
  client?: string;
  failed?: boolean;
  keyword?: string;
  page: number;
  pageSize: number;
  range: AgentSessionRange;
  /** Explicit window start (unix s); wins over range. */
  startTimestamp?: number;
  tokenName?: string;
  /** Operator only: narrow to one account (internal id). */
  userId?: number | string;
};

const EMPTY_SUMMARY: AgentSessionListSummary = {
  amount_microusd: 0,
  cached_tokens: 0,
  compared_microusd: 0,
  completion_tokens: 0,
  failed_sessions: 0,
  median_amount_microusd: null,
  median_duration_seconds: null,
  median_requests: null,
  official_microusd: 0,
  prompt_tokens: 0,
  request_count: 0,
  sessions: 0,
  subagents: 0,
  tokens: 0,
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === 'object' && !Array.isArray(value);

async function fetchAgentSessions(
  params: AgentSessionListParams,
): Promise<AgentSessionList> {
  const base = params.admin ? '/api/log/sessions' : '/api/log/self/sessions';
  const query = createSearchParams({
    client: params.client,
    failed: params.failed ? '1' : undefined,
    keyword: params.keyword,
    p: params.page,
    page_size: params.pageSize,
    range: params.range,
    start_timestamp: params.startTimestamp,
    token_name: params.tokenName,
    user_id: params.admin ? params.userId : undefined,
  });
  const data = unwrap<unknown>(await API.get(`${base}?${query}`));
  const page = isRecord(data) && isRecord(data.page) ? data.page : {};
  const summary =
    isRecord(data) && isRecord(data.summary)
      ? (data.summary as AgentSessionListSummary)
      : EMPTY_SUMMARY;
  const items = Array.isArray(page.items)
    ? (page.items as AgentSessionListItem[]).map((item) => ({
        ...item,
        models: Array.isArray(item.models) ? item.models : [],
      }))
    : [];
  return {
    items,
    page: Number(page.page) || params.page,
    pageSize: Number(page.page_size) || params.pageSize,
    summary: { ...EMPTY_SUMMARY, ...summary },
    total: Number(page.total) || 0,
  };
}

async function fetchAgentSessionDetail(
  admin: boolean,
  id: string,
  cursor = '',
): Promise<AgentSessionDetail> {
  const path = admin
    ? `/api/log/sessions/${encodeURIComponent(id)}`
    : `/api/log/self/sessions/${encodeURIComponent(id)}`;
  const data = unwrap<AgentSessionDetail>(
    await API.get(
      `${path}?${createSearchParams({ request_cursor: cursor || undefined })}`,
    ),
  );
  return {
    ...data,
    first_message: data.first_message || '',
    models: Array.isArray(data.models) ? data.models : [],
    requests: Array.isArray(data.requests) ? data.requests : [],
    threads: Array.isArray(data.threads) ? data.threads : [],
  };
}

export function useAgentSessionsQuery(
  params: AgentSessionListParams & { enabled?: boolean },
) {
  const { enabled = true, ...queryParams } = params;
  return useQuery({
    enabled,
    queryKey: queryKeys.console.table('agentSessions', queryParams),
    queryFn: () => fetchAgentSessions(queryParams),
    placeholderData: keepPreviousData,
  });
}

export function useAgentSessionDetailQuery({
  admin,
  enabled = true,
  id,
}: {
  admin: boolean;
  enabled?: boolean;
  id: string;
}) {
  const query = useInfiniteQuery({
    enabled: enabled && Boolean(id),
    queryKey: queryKeys.console.detail('agentSession', [admin, id]),
    initialPageParam: '',
    queryFn: ({ pageParam }) => fetchAgentSessionDetail(admin, id, pageParam),
    getNextPageParam: (last) => last.next_request_cursor || undefined,
  });
  const pages = query.data?.pages;
  const first = pages?.[0];
  const requests = pages?.flatMap((page) => page.requests) ?? [];
  return {
    ...query,
    data: first
      ? {
          ...first,
          requests,
          next_request_cursor: pages?.[pages.length - 1]?.next_request_cursor,
          requests_complete:
            first.requests_available === first.session.request_count &&
            requests.length === first.session.request_count,
        }
      : undefined,
  };
}

/**
 * A request log row carries the raw session id; the detail route takes the
 * session's public id. The list endpoint matches `keyword` against both, and
 * any session holding a request made at `at` has last_at >= at, so a window
 * starting there always contains it.
 */
export function useAgentSessionLookupQuery({
  admin,
  at,
  enabled = true,
  sessionId,
  userId,
}: {
  admin: boolean;
  at: number;
  enabled?: boolean;
  sessionId: string;
  userId?: number | string;
}) {
  return useQuery({
    enabled: enabled && Boolean(sessionId),
    queryKey: queryKeys.console.detail('agentSessionLookup', [
      admin,
      sessionId,
      at,
      userId ?? '',
    ]),
    queryFn: async () => {
      const list = await fetchAgentSessions({
        admin,
        keyword: sessionId,
        page: 1,
        pageSize: 10,
        range: '90d',
        startTimestamp: at > 0 ? at - 1 : undefined,
        userId,
      });
      return list.items.find((item) => item.session_id === sessionId) ?? null;
    },
  });
}

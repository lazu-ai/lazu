import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useMemo } from 'react';
import { notify } from '../../components/ui/toast';
import { API } from '../../helpers/api';
import { getUserIdFromLocalStorage, showError } from '../../helpers/utils';
import {
  apiErrorHandler,
  apiErrorHandlerWith,
  unwrap,
} from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import type { SellPriceRow } from '../../pages/ModelOps/sellPrices';
import type { AxiosResponse } from 'axios';
import type { TFunction } from 'i18next';

/** Supply-side layers the 模型运营 page reads. Everything in here maps 1:1 onto a
 *  real backend field — see /api/model_ops in internal/controller/model_ops.go. */

export type ModelOpsLane = 'cheap' | 'direct';

export type ModelOpsSourceType =
  | 'aggregator'
  | 'cloud'
  | 'official'
  | 'reverse'
  /** No one has classified this source yet. The backfill never guesses, so this
   *  is what every pre-existing channel reports until an operator decides. */
  | 'unclassified';

export type ModelOpsProbeBucket = {
  end_at: string;
  error_count: number;
  request_count: number;
  start_at: string;
  success_rate: number | null;
};

export type ModelOpsHealth = {
  avg_latency_ms: number | null;
  buckets?: ModelOpsProbeBucket[];
  error_count: number;
  last_outcome_at: string | null;
  request_count: number;
  /** "probe" when the route carried no traffic in the window and the record
   *  is its probes; absent for a traffic record. */
  source?: 'probe' | 'traffic';
  success_rate: number | null;
};

export type ModelOpsObservationState = 'fail' | 'na' | 'pass' | 'pending';

export type ModelOpsObservationCheckKind = 'billing' | 'long_context' | 'tools';

export type ModelOpsObservationCheck = {
  detail?: string;
  kind: ModelOpsObservationCheckKind;
  passed: number;
  runs: number;
  state: ModelOpsObservationState;
  /** Billing: the worst billed-to-actual ratio seen. */
  value: number | null;
  /** Billing: the ratio of the most recent run. */
  latest?: number | null;
};

/** Where a watched route — or a source's watched routes together — stands in
 *  its observation before going live. */
export type ModelOpsObservation = {
  checks: ModelOpsObservationCheck[];
  complete: boolean;
  day: number;
  days_required: number;
  /* The bars the server judges a watched route by (source_checks in the
     backend config), sent so the console states them without a copy. */
  /** The long-context check's standard input size, in tokens. */
  long_context_tokens?: number;
  /** The least liveness record that can back a success-rate verdict. */
  min_samples?: number;
  /** The share of liveness probes that must succeed. */
  min_success_rate?: number;
  /** Routes of the source that already carry traffic (source rows only). */
  live_routes?: number;
  /** Distinct models under observation (source rows only). */
  models?: number;
  ready: boolean;
  routes?: number;
  samples: number;
  /** Failed probes among `samples`. */
  failures?: number;
  started_at: string;
  success_rate: number | null;
  success_state: ModelOpsObservationState;
  /** Source rows only: the routes behind the aggregate, as the ledger line
   *  reads them. */
  summary?: ModelOpsObservationSummary;
};

/** What holds one watched route back: its probe record ("probe") or one of
 *  the three checks. */
export type ModelOpsObservationReason = {
  detail?: string;
  kind: 'probe' | ModelOpsObservationCheckKind;
  value?: number | null;
};

export type ModelOpsObservationSummary = {
  all_passed: number;
  pending: number;
  problem_count: number;
  problems: Array<ModelOpsObservationReason & { model: string }>;
  ready: number;
  /** The watched model whose probes did worst in the window, when below the
   *  bar. */
  worst_probe?: { failures: number; model: string; samples: number };
};

export type ModelOpsObservationVerdict = 'problem' | 'ready' | 'watching';

/** One watched route of a source, as the observation dialog draws it. */
export type ModelOpsObservationRoute = {
  /** Per observation day so far, the last result of each check that day. */
  days: Array<
    Partial<Record<ModelOpsObservationCheckKind, 'fail' | 'na' | 'pass'>>
  >;
  lane: ModelOpsLane;
  model: string;
  observation: ModelOpsObservation;
  reason?: ModelOpsObservationReason;
  /** Two-hour probe slots from `slot_origin` up to now. */
  slot_origin: string;
  slots: Array<{ failures: number; samples: number }>;
  verdict: ModelOpsObservationVerdict;
};

export type ModelOpsSourceObservationDetail = {
  now: string;
  routes: ModelOpsObservationRoute[];
  slot_seconds: number;
  slots: number;
  source: { id: string; lane: ModelOpsLane; name: string; status: number };
};

export type ModelOpsRouteRole = 'backup' | 'inventory' | 'primary';

export type ModelOpsSource = {
  auto_ban: boolean;
  base_url: string;
  cost_input_per_mtok: number | null;
  cost_output_per_mtok: number | null;
  cost_request_usd?: number | null;
  cost_row_count: number;
  cost_second_usd?: number | null;
  endpoint: string;
  health: ModelOpsHealth;
  id: string;
  internal_id: number;
  key_fingerprint: string;
  lane: ModelOpsLane;
  model_count: number;
  model_names?: string[];
  name: string;
  /** True while nobody has decided what this source is. The migration refuses
   *  to guess, so this is how the ledger finds the rows awaiting a decision. */
  needs_classification: boolean;
  observation?: ModelOpsObservation;
  priority: number;
  response_time_ms: number;
  source_type: ModelOpsSourceType;
  status: number;
  test_time: number;
  type: number;
  type_name: string;
  weight: number;
};

export type ModelOpsSourceRules = {
  auto_test_channel_enabled: boolean;
  auto_test_channel_minutes: number;
  automatic_disable_enabled: boolean;
  automatic_enable_enabled: boolean;
  channel_disable_threshold_seconds: number;
  outcome_telemetry_available: boolean;
  window_hours: number;
};

export type ModelOpsPoolEntry = {
  channel_id: string;
  channel_internal_id: number;
  channel_status: number;
  cost_input_per_mtok: number | null;
  cost_output_per_mtok: number | null;
  cost_request_usd?: number | null;
  cost_second_usd?: number | null;
  enabled: boolean;
  health: ModelOpsHealth;
  lane: ModelOpsLane;
  name: string;
  observation?: ModelOpsObservation;
  priority: number;
  response_time_ms: number;
  role?: ModelOpsRouteRole;
  source_type: ModelOpsSourceType;
  type: number;
  weight: number;
};

export type ModelOpsPools = Record<
  string,
  Partial<Record<ModelOpsLane, ModelOpsPoolEntry[]>>
>;

const EMPTY_POOLS: ModelOpsPools = {};
const EMPTY_SOURCES: ModelOpsSource[] = [];
const THIRTY_DAYS_SECONDS = 30 * 24 * 60 * 60;

/** A response's `data`, or `fallback` when the server sent none. A failure is
 *  never turned into the fallback: non-2xx rejects in axios and a stray
 *  `success: false` throws from unwrap, so the query reports it. */
const readPayload = <T,>(response: AxiosResponse<unknown>, fallback: T): T =>
  unwrap<T | null | undefined>(response) ?? fallback;

/** Per-model, per-lane source pools with live health. Requested for exactly the
 *  model names on screen so the query stays bounded by the page size. */
export function useModelOpsPoolsQuery(modelNames: string[]) {
  const key = useMemo(
    () => [...modelNames].sort().join(','),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [modelNames.join(',')],
  );
  const query = useQuery(modelOpsPoolsQueryOptions(key));
  useEffect(() => {
    if (query.error) showError(query.error);
  }, [query.error]);
  return query;
}

export function modelOpsPoolsQueryOptions(key: string) {
  return {
    enabled: key.length > 0,
    queryKey: queryKeys.console.table('model-ops-pools', {
      key,
      accountId: getUserIdFromLocalStorage(),
    }),
    queryFn: async (): Promise<ModelOpsPools> => {
      const response = await API.get(
        `/api/model_ops/pools?models=${encodeURIComponent(key)}`,
        { skipErrorHandler: true },
      );
      const data = readPayload<{ items?: ModelOpsPools }>(response, {});
      return data.items || EMPTY_POOLS;
    },
    staleTime: 30 * 1000,
  };
}

export function useModelOpsSourcesQuery(modelName = '', windowHours = 48) {
  const model = modelName.trim();
  return useQuery({
    queryKey: queryKeys.console.table('model-ops-sources', {
      model,
      windowHours,
    }),
    queryFn: async (): Promise<{
      items: ModelOpsSource[];
      rules: ModelOpsSourceRules | null;
    }> => {
      const response = await API.get(
        `/api/model_ops/sources?window=${windowHours}h${model ? `&model=${encodeURIComponent(model)}` : ''}`,
      );
      const data = readPayload<{
        items?: ModelOpsSource[];
        rules?: ModelOpsSourceRules;
      }>(response, {});
      return { items: data.items || EMPTY_SOURCES, rules: data.rules || null };
    },
    staleTime: 30 * 1000,
  });
}

/** Every watched route of one source, slot by slot and day by day, for the
 *  observation dialog. */
export function useModelOpsSourceObservationQuery(sourceId: string | null) {
  return useQuery({
    enabled: Boolean(sourceId),
    queryKey: queryKeys.console.detail('model-ops-observation', sourceId || ''),
    queryFn: async (): Promise<ModelOpsSourceObservationDetail> => {
      const response = await API.get(
        `/api/model_ops/sources/${encodeURIComponent(String(sourceId))}/observation`,
      );
      return unwrap<ModelOpsSourceObservationDetail>(response);
    },
    staleTime: 30 * 1000,
  });
}

/** Sell prices are RootAuth. Non-root admins get an empty list and the tier
 *  editor renders read-only rather than firing a request that 403s. */
export function useModelOpsSellPricesQuery(enabled: boolean) {
  return useQuery({
    enabled,
    queryKey: queryKeys.console.table('model-ops-sell-prices', {}),
    queryFn: async (): Promise<SellPriceRow[]> => {
      const response = await API.get('/api/pricing/sell_prices');
      const data = unwrap<unknown>(response);
      return Array.isArray(data) ? (data as SellPriceRow[]) : [];
    },
  });
}

/** Model names the product actually offers, straight from the catalog.
 *
 *  The 折扣 matrix used to build its rows from every row in the sell-price
 *  table, which is a ledger and not a roster: it carried a price for anything
 *  ever priced, including 160-odd models from the inherited seed list that the
 *  product has never served. /api/pricing applies the catalog's own 上架 gate,
 *  so it answers "which models can a customer actually buy". */
/** How many models the 模型 table lists under 在售 — read from the same
 *  admin list and status filter the table uses, so the tab's count and the
 *  table's rows can never disagree. (The public catalog counts something
 *  else: models that are both offered and buyable right now.) */
export function useOfferedModelCountQuery(enabled: boolean) {
  return useQuery({
    enabled,
    queryKey: queryKeys.console.table('model-ops-offered-count', {}),
    staleTime: 60 * 1000,
    queryFn: async (): Promise<number> => {
      const response = await API.get(
        '/api/models/search?p=1&page_size=1&status=offered',
        { skipErrorHandler: true },
      );
      const data = unwrap<{ total?: unknown }>(response);
      const total = Number(data?.total);
      if (!Number.isFinite(total)) {
        throw new Error('Invalid offered-model count');
      }
      return total;
    },
  });
}

export type SyncPreviewKind = 'litellm' | 'openrouter' | 'upstream';

const PREVIEW_PATHS: Record<SyncPreviewKind, string> = {
  litellm: '/api/models/sync_litellm/preview',
  openrouter: '/api/models/sync_openrouter/preview',
  upstream: '/api/models/sync_upstream/preview',
};

export type SyncPreviewData = {
  conflicts?: Array<{
    fields?: Array<{ field: string; local: unknown; upstream: unknown }>;
    model_name: string;
  }>;
  litellm_total?: number;
  local_total?: number;
  matched?: Array<Record<string, unknown>>;
  matched_count?: number;
  missing?: string[];
  openrouter_total?: number;
  unmatched?: string[];
  unmatched_count?: number;
};

/** Dry run before a bulk write. Every sync button used to POST straight into a
 *  catalog-wide overwrite with no way to see what it would change first. */
export function useSyncPreviewQuery(kind: SyncPreviewKind | null) {
  return useQuery({
    enabled: Boolean(kind),
    gcTime: 0,
    queryKey: queryKeys.console.detail(
      'model-ops-sync-preview',
      kind || 'none',
    ),
    queryFn: async (): Promise<SyncPreviewData> => {
      const response = await API.get(PREVIEW_PATHS[kind as SyncPreviewKind], {
        skipErrorHandler: true,
      });
      return unwrap<SyncPreviewData | null>(response) || {};
    },
    retry: false,
    staleTime: 0,
  });
}

export function useModelOpsMutations(t: TFunction) {
  const queryClient = useQueryClient();
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: queryKeys.console.root });

  /** Pool-local weight = abilities.weight for one (lane, model, source). This is
   *  the value the router's weighted pick actually reads. */
  const poolWeightMutation = useMutation({
    mutationFn: async (variables: {
      channelId: string;
      lane: ModelOpsLane;
      model: string;
      weight: number;
    }) => {
      const response = await API.put('/api/model_ops/pool_weight', {
        channel_id: variables.channelId,
        lane: variables.lane,
        model: variables.model,
        weight: variables.weight,
      });
      return unwrap<unknown>(response);
    },
    onSuccess: () => {
      notify.success(t('page.console.modelOps.weightSaved'));
      invalidate();
    },
    onError: apiErrorHandlerWith(t('page.console.modelOps.weightFailed')),
  });

  /** Global default weight = channels.weight. Existing pool-local abilities
   *  keep their overrides; newly added memberships inherit this value. */
  const sourceWeightMutation = useMutation({
    mutationFn: async (variables: {
      source: ModelOpsSource;
      weight: number;
    }) => {
      const response = await API.put('/api/channel/', {
        group: variables.source.lane,
        id: variables.source.id,
        weight: Math.max(0, variables.weight),
      });
      return unwrap<unknown>(response);
    },
    onSuccess: () => {
      notify.success(t('page.console.modelOps.weightSaved'));
      invalidate();
    },
    onError: apiErrorHandlerWith(t('page.console.modelOps.weightFailed')),
  });

  const batchStatusMutation = useMutation({
    mutationFn: async (variables: {
      ids: Array<number | string>;
      status: 0 | 1;
    }) => {
      const response = await API.put('/api/model_ops/models/status', {
        ids: variables.ids.map((id) => Number(id)).filter(Number.isFinite),
        status: variables.status,
      });
      const data = unwrap<{ updated?: number } | null>(response);
      return data?.updated ?? variables.ids.length;
    },
    onSuccess: (updated) => {
      notify.success(
        t('page.console.modelOps.batchStatusDone', {
          count: Number(updated) || 0,
          defaultValue: '已更新 {{count}} 个模型的上架状态',
        }),
      );
      invalidate();
    },
    onError: apiErrorHandler,
  });

  const testSourceMutation = useMutation({
    mutationFn: async (id: string) => {
      return unwrap<unknown>(await API.get(`/api/channel/test/${id}?model=`));
    },
    onSuccess: () => {
      notify.success(
        t('page.console.modelOps.testDone', { defaultValue: '测试完成' }),
      );
      invalidate();
    },
    onError: apiErrorHandler,
  });

  const testAllSourcesMutation = useMutation({
    mutationFn: async () => {
      return unwrap<unknown>(await API.get('/api/channel/test'));
    },
    onSuccess: () => {
      notify.success(
        t('page.console.modelOps.testAllStarted', {
          defaultValue: '已开始测试全部来源',
        }),
      );
    },
    onError: apiErrorHandler,
  });

  const sourceStatusMutation = useMutation({
    mutationFn: async (variables: {
      enabled: boolean;
      source: ModelOpsSource;
    }) => {
      const response = await API.put('/api/channel/', {
        group: variables.source.lane,
        id: variables.source.id,
        status: variables.enabled ? 1 : 2,
      });
      return unwrap<unknown>(response);
    },
    onSuccess: () => {
      notify.success(t('page.console.tables.actionSuccess'));
      invalidate();
    },
    onError: apiErrorHandler,
  });

  const createVendorMutation = useMutation({
    mutationFn: async (name: string) => {
      const response = await API.post('/api/vendors/', {
        name: name.trim(),
        status: 1,
      });
      return unwrap<{ id?: string; internal_id?: number }>(response);
    },
    onSuccess: () => {
      notify.success(t('page.console.modelOps.vendorCreated'));
      queryClient.invalidateQueries({
        queryKey: queryKeys.console.detail('models', 'vendors'),
      });
    },
    onError: apiErrorHandlerWith(t('page.console.modelOps.vendorCreateFailed')),
  });

  /** Watched routes of one source go live: the named models, or all of them.
   *  The server refuses unless every one asked for has passed. */
  const goLiveSourceMutation = useMutation({
    mutationFn: async (variables: {
      models?: string[];
      source: Pick<ModelOpsSource, 'id' | 'name'>;
    }) => {
      const response = await API.post(
        `/api/model_ops/sources/${encodeURIComponent(variables.source.id)}/go_live`,
        variables.models?.length
          ? { models: variables.models, role: 'backup' }
          : { role: 'backup' },
      );
      return unwrap<unknown>(response);
    },
    onSuccess: (_data, variables) => {
      notify.success(
        t('page.console.modelOps.observation.done', {
          defaultValue: '{{name}} 已上线为备用',
          name: variables.source.name,
        }),
      );
      invalidate();
    },
    onError: apiErrorHandler,
  });

  /** Watched models leave a source's model list; serving ones are refused. */
  const removeWatchedModelsMutation = useMutation({
    mutationFn: async (variables: {
      models: string[];
      source: Pick<ModelOpsSource, 'id' | 'name'>;
    }) => {
      const response = await API.post(
        `/api/model_ops/sources/${encodeURIComponent(variables.source.id)}/remove_models`,
        { models: variables.models },
      );
      return unwrap<unknown>(response);
    },
    onSuccess: (_data, variables) => {
      notify.success(
        t('page.console.modelOps.observation.removed', {
          count: variables.models.length,
          defaultValue: '已从 {{name}} 移除 {{count}} 个模型',
          name: variables.source.name,
        }),
      );
      invalidate();
    },
    onError: apiErrorHandler,
  });

  /** One watched route goes live on one model's tier. */
  const goLiveRouteMutation = useMutation({
    mutationFn: async (variables: {
      entry: ModelOpsPoolEntry;
      lane: ModelOpsLane;
      model: string;
    }) => {
      const response = await API.put('/api/channel_health/route_role', {
        channel_id: String(variables.entry.channel_id),
        lane: variables.lane,
        model: variables.model,
        role: 'backup',
      });
      return unwrap<unknown>(response);
    },
    onSuccess: (_data, variables) => {
      notify.success(
        t('page.console.modelOps.observation.done', {
          defaultValue: '{{name}} 已上线为备用',
          name: variables.entry.name,
        }),
      );
      invalidate();
    },
    onError: apiErrorHandler,
  });

  /** A backup on one model's tier becomes one of its primaries and starts
   *  sharing traffic by weight. */
  const promoteRouteMutation = useMutation({
    mutationFn: async (variables: {
      entry: ModelOpsPoolEntry;
      lane: ModelOpsLane;
      model: string;
    }) => {
      const response = await API.put('/api/channel_health/route_role', {
        channel_id: String(variables.entry.channel_id),
        lane: variables.lane,
        model: variables.model,
        role: 'primary',
      });
      return unwrap<unknown>(response);
    },
    onSuccess: (_data, variables) => {
      notify.success(
        t('page.console.modelOps.tier.promoteDone', {
          defaultValue: '{{name}} 已升为主用',
          name: variables.entry.name,
        }),
      );
      invalidate();
    },
    onError: apiErrorHandler,
  });

  return {
    batchStatusMutation,
    createVendorMutation,
    goLiveRouteMutation,
    promoteRouteMutation,
    goLiveSourceMutation,
    invalidate,
    removeWatchedModelsMutation,
    poolWeightMutation,
    sourceStatusMutation,
    sourceWeightMutation,
    testAllSourcesMutation,
    testSourceMutation,
  };
}

export const MODEL_OPS_SPEND_WINDOW_SECONDS = THIRTY_DAYS_SECONDS;

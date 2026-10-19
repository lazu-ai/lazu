import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { API } from '../../helpers/api';
import { getUserIdFromLocalStorage } from '../../helpers/utils';
import { unwrap } from '../../lib/api-error';
import { queryKeys } from '../../lib/query-keys';
import type { AxiosResponse } from 'axios';

/** Where a reading came from. A cell holds only what was measured inside
 *  its own slice — a customer request or a probe, drawn the same way — or
 *  nothing; 'blocked' means we were structurally unable to look at all, which
 *  is a gap in our own setup rather than anything about the upstream. */
export type ChannelHealthSource =
  | 'traffic'
  | 'probe'
  /** What a member receives instead of traffic or probe: which samples were
   *  ours is an operations fact. */
  | 'sample'
  | 'blocked'
  | 'none';

/** What a row's model produces. Each kind is measured by its own figures and
 *  drawn in its own section; audio and video appear once such models ship. */
export type ChannelHealthKind =
  | 'llm'
  | 'embedding'
  | 'rerank'
  | 'decision'
  | 'image'
  | 'audio'
  | 'video'
  | 'search';

/** One section of the page and the slice width its wall is drawn in. Image
 *  generation is sampled hours apart, so its slices are three times wider. */
export type ChannelHealthKindSection = {
  kind: ChannelHealthKind;
  bucket_count: number;
  bucket_seconds: number;
  row_count: number;
};

export type ChannelHealthStatus =
  | 'operational'
  | 'degraded'
  | 'outage'
  | 'unknown';

export type ChannelHealthTone = 'ok' | 'warn' | 'bad' | 'blocked' | 'none';

/** What an upstream is FOR on one route, which is a different question from
 *  how much of the load it takes. */
export type ChannelHealthRole = 'primary' | 'backup' | 'inventory';

export type ChannelHealthBucket = {
  index: number;
  start_at: string;
  source: ChannelHealthSource;
  request_count: number;
  error_count: number;
  success_rate?: number;
  tone: ChannelHealthTone;
};

/** One upstream channel serving a lane. Admin-only: users see lanes, not the
 *  vendors behind them. */
export type ChannelHealthChannel = {
  channel_id: number;
  channel_name: string;
  status: ChannelHealthStatus;
  source: ChannelHealthSource;
  weight: number;
  /** Customer requests; probe_count is the synthetic half of the samples. */
  request_count: number;
  probe_count?: number;
  success_rate?: number;
  avg_latency_ms?: number;
  share?: number;
  disabled: boolean;
  role?: ChannelHealthRole;
  /** The breaker's running count of failed samples since the last success. */
  consecutive_failures?: number;
  /** The routed name when this route serves an aliased variant of the row's
   *  catalog model. Admin writes to the route must send it. */
  ability_model?: string;
  /** The breaker has pulled this route; retreat_until is when it is retried. */
  retreating?: boolean;
  retreat_until?: string;
  /** Why this route cannot be probed at all, reported verbatim. */
  blocked_reason?: string;
};

export type ChannelHealthRow = {
  model_name: string;
  /** The lab behind the model; rows arrive ordered by maker, newest first. */
  maker?: string;
  maker_rank?: number;
  release_date?: string;
  /** Absent only on a snapshot written before kinds existed; read as 'llm'. */
  kind?: ChannelHealthKind;
  /** 'stable' or 'eco' — the wire names the model catalog already uses. */
  lane: string;
  /** The lane's real registry slug. Admin-only, and the only safe thing to
   *  send back when writing to a route: `lane` collapses every non-agent lane
   *  to 'stable', so deriving the slug from it would edit the wrong route on
   *  any operator-created lane. */
  lane_slug?: string;
  /** The router's breaker verdict over the upstreams it would choose now —
   *  not a threshold on success_rate. */
  status: ChannelHealthStatus;
  source: ChannelHealthSource;
  /** Customer requests and their failures in the window. */
  request_count: number;
  error_count: number;
  /** Share of samples — customer requests and probes alike — that actually
   *  returned inference. A 200 carrying no output tokens is a failure. */
  success_rate?: number;
  avg_latency_ms?: number;
  p95_latency_ms?: number;
  ttft_p50_ms?: number;
  /** The slow tail of the first token. Unlike whole-reply duration it does
   *  not grow with how much the caller asked for. */
  ttft_p95_ms?: number;
  /** Whole-request duration over successful samples — the speed figures of
   *  kinds without a stream: embeddings, images, search. */
  latency_p50_ms?: number;
  latency_p95_ms?: number;
  output_tps?: number;
  /** Streamed replies that broke after output had started, over streamed
   *  replies that started at all. */
  interruption_rate?: number;
  /** Median of each reply's longest gap between output chunks. Average TPS
   *  hides a lane that streams fast and then pauses for six seconds. */
  stall_p50_ms?: number;
  /** The lane's list price, per million tokens, so the two lanes of one
   *  model can be compared on price and speed in the same row. */
  input_per_mtok?: number;
  output_per_mtok?: number;
  last_outcome_at?: string;
  last_probed_at?: string;
  /** Admin-only: the synthetic half of the samples. */
  probe_count?: number;
  probe_error_count?: number;
  channel_count: number;
  buckets: ChannelHealthBucket[];
  sources?: ChannelHealthChannel[];
  /** Admin-only. A standby is carrying this route because every primary is
   *  out of rotation. The lane still works, which is why the customer view
   *  never receives this. */
  fallback_active?: boolean;
  /** Admin-only: how many upstreams the router is choosing between. */
  serving_count?: number;
  /** Admin-only: how many of this route's upstreams cannot be checked. */
  blocked_count?: number;
};

/** One route an upstream serves, seen from the upstream's side. */
export type ChannelHealthProviderLane = {
  model_name: string;
  lane: string;
  status: ChannelHealthStatus;
  source: ChannelHealthSource;
  success_rate?: number;
  request_count: number;
  share?: number;
  weight: number;
  disabled: boolean;
};

/** The newest synthetic check of an upstream, reported verbatim. An idle
 *  source has no traffic to speak for it, so this is the whole evidence. */
export type ChannelHealthLastProbe = {
  probed_at: string;
  model_name?: string;
  lane?: string;
  success: boolean;
  latency_ms?: number;
  error?: string;
};

/** The same window aggregated by upstream instead of by lane. Admin-only, and
 *  absent (not empty) from a member's payload. */
export type ChannelHealthProvider = {
  channel_id: number;
  channel_name: string;
  source_type: string;
  channel_type: number;
  status: ChannelHealthStatus;
  source: ChannelHealthSource;
  disabled: boolean;
  weight: number;
  request_count: number;
  error_count: number;
  success_rate?: number;
  avg_latency_ms?: number;
  p95_latency_ms?: number;
  output_tps?: number;
  share?: number;
  model_count: number;
  lane_count: number;
  probe_count: number;
  last_outcome_at?: string;
  last_probed_at?: string;
  last_probe?: ChannelHealthLastProbe;
  buckets: ChannelHealthBucket[];
  lanes: ChannelHealthProviderLane[];
};

export type ChannelHealthSummary = {
  stable_success_rate?: number;
  eco_success_rate?: number;
  output_tps?: number;
  active_issues: number;
  degraded_count: number;
  outage_count: number;
  no_data_count: number;
  lane_count: number;
  model_count: number;
  request_count: number;
  /** Admin-only, and omitted rather than zeroed for members. */
  provider_count?: number;
  provider_degraded_count?: number;
  provider_outage_count?: number;
  fallback_count?: number;
  blocked_count?: number;
};

export type ChannelHealthPayload = {
  window: string;
  window_seconds: number;
  bucket_count: number;
  bucket_seconds: number;
  is_admin: boolean;
  summary: ChannelHealthSummary;
  /** The sections present, in display order. */
  kinds?: ChannelHealthKindSection[];
  rows: ChannelHealthRow[];
  /** Absent entirely for members — the by-upstream view is admin-only. */
  providers?: ChannelHealthProvider[];
  updated_at: string;
};

/** A day and a week. An hour holds one probe per idle route, and a month is a
 *  reporting question the usage pages answer. */
export const CHANNEL_HEALTH_WINDOWS = ['24h', '7d'] as const;
export type ChannelHealthWindow = (typeof CHANNEL_HEALTH_WINDOWS)[number];

/* Unlike the other console hooks, this one throws on an unsuccessful envelope
   rather than degrading to an empty result. On a monitoring surface the two are
   not interchangeable: "no channels have any data" and "we could not read the
   health of anything" look identical as an empty table, and the second one is
   the state an operator most needs to see. */
const readChannelHealth = (
  response: AxiosResponse<unknown>,
): ChannelHealthPayload => {
  const payload = unwrap<ChannelHealthPayload | null>(response);
  // A 200 without data is not an empty board; the page shows its own copy.
  if (!payload) throw new Error('channel health response carried no data');
  return payload;
};

export const channelHealthQueryKey = (window: ChannelHealthWindow) =>
  queryKeys.console.table('channel-health', {
    window,
    accountId: getUserIdFromLocalStorage(),
  });

/** Move one upstream between carrying a route, standing by, and inventory. */
export function useChannelHealthRoleMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: {
      channel_id: number;
      lane: string;
      model: string;
      role: ChannelHealthRole;
    }) => {
      const response = await API.put('/api/channel_health/route_role', {
        ...input,
        channel_id: String(input.channel_id),
      });
      return unwrap<unknown>(response);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.console.table('channel-health', {}),
        exact: false,
      });
    },
  });
}

/** End a retreat now rather than waiting out its deadline, for when the
 *  operator has just fixed the thing the breaker was reacting to. */
export function useChannelHealthRetreatMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: {
      channel_id: number;
      lane: string;
      model: string;
    }) => {
      const response = await API.post(
        '/api/channel_health/route_retreat/clear',
        { ...input, channel_id: String(input.channel_id) },
      );
      return unwrap<unknown>(response);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.console.table('channel-health', {}),
        exact: false,
      });
    },
  });
}

export function channelHealthQueryOptions(window: ChannelHealthWindow = '24h') {
  return {
    queryKey: channelHealthQueryKey(window),
    queryFn: async (): Promise<ChannelHealthPayload> => {
      const response = await API.get(
        `/api/channel_health?window=${encodeURIComponent(window)}`,
        { skipErrorHandler: true },
      );
      return readChannelHealth(response);
    },
    // The server already caches the aggregate for 30s, so a shorter client
    // staleTime would only re-fetch the same bytes.
    staleTime: 30 * 1000,
  };
}

export function useChannelHealthQuery(window: ChannelHealthWindow = '24h') {
  return useQuery(channelHealthQueryOptions(window));
}

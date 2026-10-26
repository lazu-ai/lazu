import { useQuery } from '@tanstack/react-query';
import { API } from '../../../helpers/api';
import { unwrap } from '../../../lib/api-error';

/** Mirrors internal/model/overview.go. Money is microUSD. */
export type OverviewTotals = {
  request_count: number;
  outcome_count: number;
  error_count: number;
  success_rate: number | null;
  amount_microusd: number;
  upstream_cost_microusd?: number;
  gross_margin_microusd?: number;
  cost_sample_count?: number;
  prompt_tokens: number;
  completion_tokens: number;
  cached_tokens: number;
  p95_latency_ms: number | null;
  p95_ttft_ms: number | null;
  active_users?: number;
};

export type OverviewBucket = {
  start: number;
  end: number;
  partial: boolean;
  request_count: number;
  outcome_count: number;
  error_count: number;
  amount_microusd: number;
  upstream_cost_microusd?: number;
  gross_margin_microusd?: number;
  prompt_tokens: number;
  completion_tokens: number;
  cached_tokens: number;
  active_users?: number;
  new_users?: number;
  returned_users?: number;
  churned_users?: number;
};

export type OverviewGrowth = {
  new_users: number;
  activated: number;
  paid: number;
  active_users: number;
  returned_users: number;
  churned_users: number;
  total_customers: number;
  churn_applicable: boolean;
  new_users_previous: number;
  active_users_previous: number;
  sources?: Array<{
    source: string;
    users: number;
    activated: number;
    paid: number;
  }>;
  campaigns?: Array<{
    source: string;
    users: number;
    activated: number;
    paid: number;
  }>;
  landing_pages?: Array<{
    source: string;
    users: number;
    activated: number;
    paid: number;
  }>;
};

export type OverviewDimensionRow = {
  id: number;
  name: string;
  request_count: number;
  error_count?: number;
  amount_microusd: number;
  upstream_cost_microusd?: number;
  gross_margin_microusd?: number;
  gross_margin_ratio?: number;
  prompt_tokens: number;
  completion_tokens: number;
  cached_input_tokens: number;
  last_used_at: number;
};

export type OverviewClient = {
  client: string;
  users: number;
  sessions: number;
  request_count: number;
  tokens: number;
  amount_microusd: number;
};

export type OverviewData = {
  start: number;
  end: number;
  bucket: 'hour' | 'day' | 'week';
  tz_offset: number;
  totals: OverviewTotals;
  previous: OverviewTotals;
  series: OverviewBucket[];
  growth?: OverviewGrowth;
  clients?: OverviewClient[];
  sessions?: {
    sessions: number;
    median_amount_microusd: number | null;
    median_duration_seconds: number | null;
    subagent_share: number | null;
  };
  models: OverviewDimensionRow[];
  keys?: OverviewDimensionRow[];
  users?: OverviewDimensionRow[];
  topups?: {
    count: number;
    amount_microusd: number;
    recent: Array<{
      username: string;
      amount_microusd: number;
      completed_at: number;
    }>;
  };
};

export type OverviewPreset = '24h' | '7d' | '30d' | '90d';
export type OverviewRange =
  | { preset: OverviewPreset }
  | { preset: 'custom'; start: number; end: number };

export const OVERVIEW_PRESETS: OverviewPreset[] = ['24h', '7d', '30d', '90d'];

/** The viewer's UTC offset in minutes (east positive), for local midnights. */
export function viewerTzMinutes(): number {
  return -new Date().getTimezoneOffset();
}

export function useOverviewQuery(
  scope: 'platform' | 'self',
  range: OverviewRange,
  enabled = true,
) {
  const tz = viewerTzMinutes();
  return useQuery({
    queryKey: [
      'overview',
      scope,
      range.preset,
      range.preset === 'custom' ? range.start : 0,
      range.preset === 'custom' ? range.end : 0,
      tz,
    ],
    enabled,
    queryFn: async () => {
      const params: Record<string, string | number> = { scope, tz };
      if (range.preset === 'custom') {
        params.start = range.start;
        params.end = range.end;
      } else {
        params.range = range.preset;
      }
      const response = await API.get('/api/data/overview', {
        params,
        skipErrorHandler: true,
      });
      return unwrap<OverviewData>(response);
    },
    // Hourly rollups refresh every minute; a minute-old overview is current.
    staleTime: 60_000,
    refetchInterval: 120_000,
  });
}

/** Bucket axis labels and tooltip titles in the viewer's locale. */
export function bucketLabels(data: OverviewData | undefined, locale: string) {
  const series = data?.series || [];
  const hour = new Intl.DateTimeFormat(locale, {
    hour: '2-digit',
    hour12: false,
  });
  const day = new Intl.DateTimeFormat(locale, {
    month: 'numeric',
    day: 'numeric',
  });
  const full = new Intl.DateTimeFormat(locale, {
    month: 'long',
    day: 'numeric',
    weekday: 'short',
  });
  const fullHour = new Intl.DateTimeFormat(locale, {
    month: 'long',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
  const labels = series.map((b, i) => {
    const d = new Date(b.start * 1000);
    if (data?.bucket === 'hour')
      return i === series.length - 1 && b.partial ? 'now' : `${hour.format(d)}`;
    return day.format(d);
  });
  const titles = series.map((b) => {
    const d = new Date(b.start * 1000);
    if (data?.bucket === 'hour') return fullHour.format(d);
    if (data?.bucket === 'week')
      return `${day.format(d)} – ${day.format(new Date((b.end - 86400) * 1000))}`;
    return full.format(d);
  });
  return { labels, titles, partial: series.map((b) => b.partial) };
}

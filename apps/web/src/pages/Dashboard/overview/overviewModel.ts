import {
  formatCount,
  formatMicroUsdCompact,
  formatRatio,
} from '../../../components/ui/metric-format';
import type {
  OverviewData,
  OverviewDimensionRow,
  OverviewGrowth,
  OverviewRange,
  OverviewTotals,
} from './overview-api';
import type {
  ChannelHealthProvider,
  ChannelHealthRow,
} from '../../../hooks/health/useChannelHealthQuery';

export const OVERVIEW_SUCCESS_TARGET = 0.99;
export const OVERVIEW_RUNWAY_WARN_DAYS = 7;

type TranslateFn = (key: string, options?: Record<string, unknown>) => string;

const defaultTranslate: TranslateFn = (_key, options) => {
  let out = String(options?.defaultValue ?? _key);
  if (options) {
    for (const [k, v] of Object.entries(options)) {
      if (k !== 'defaultValue') {
        out = out.replaceAll(`{{${k}}}`, String(v));
      }
    }
  }
  return out;
};

function rangeDays(range: OverviewRange, start?: number, end?: number): number {
  if (start && end && end > start) return Math.max(1, (end - start) / 86400);
  return { '24h': 1, '7d': 7, '30d': 30, '90d': 90, custom: 1 }[range.preset];
}

export function computeWorkspaceRunwayDays(
  balanceMicrousd: number | undefined,
  spendMicrousd: number,
  range: OverviewRange,
  start?: number,
  end?: number,
): number | null {
  if (balanceMicrousd === undefined) return null;
  const perDay = spendMicrousd / rangeDays(range, start, end);
  if (!(perDay > 0)) return null;
  return balanceMicrousd / perDay;
}

type WatchlistItem = {
  key: string;
  name: string;
  detail: string;
  metric: string;
  tone: 'alert' | 'warn';
  to: string;
  mono?: boolean;
};

const ns = 'page.console.overview';

export function buildPlatformWatchlist(
  {
    providers = [],
    rows = [],
    models = [],
  }: {
    providers?: ChannelHealthProvider[];
    rows?: ChannelHealthRow[];
    models?: OverviewDimensionRow[];
  },
  t: TranslateFn = defaultTranslate,
): WatchlistItem[] {
  const items: WatchlistItem[] = [];

  for (const provider of providers) {
    if (provider.disabled) continue;
    if (provider.status === 'outage' || provider.status === 'degraded') {
      const rate =
        provider.success_rate !== undefined
          ? formatRatio(provider.success_rate)
          : provider.status === 'outage'
            ? t(`${ns}.statusOutage`, { defaultValue: '中断' })
            : t(`${ns}.statusDegraded`, { defaultValue: '降级' });
      items.push({
        key: `provider-${provider.channel_id}`,
        name: provider.channel_name,
        detail:
          provider.error_count > 0
            ? t(`${ns}.watchProviderErrors`, {
                errors: formatCount(provider.error_count),
                models: formatCount(provider.model_count),
                defaultValue: '{{errors}} 次失败 · {{models}} 个模型',
              })
            : t(`${ns}.watchProviderModels`, {
                models: formatCount(provider.model_count),
                defaultValue: '{{models}} 个模型受影响',
              }),
        metric: rate,
        tone: provider.status === 'outage' ? 'alert' : 'warn',
        to: '/console/health',
      });
    }
  }

  if (items.length === 0) {
    for (const row of rows) {
      if (row.status === 'outage' || row.status === 'degraded') {
        items.push({
          key: `lane-${row.model_name}-${row.lane}`,
          name: row.model_name,
          detail: t(`${ns}.watchLaneDetail`, {
            lane: row.lane === 'eco' ? 'Agent' : 'Stable',
            errors: formatCount(row.error_count),
            defaultValue: '{{lane}} · {{errors}} 次异常',
          }),
          metric:
            row.success_rate !== undefined
              ? formatRatio(row.success_rate)
              : row.status === 'outage'
                ? t(`${ns}.statusOutage`, { defaultValue: '中断' })
                : t(`${ns}.statusDegraded`, { defaultValue: '降级' }),
          tone: row.status === 'outage' ? 'alert' : 'warn',
          to: '/console/health',
          mono: true,
        });
      }
    }
  }

  const seenModels = new Set(items.map((item) => item.name));
  const weakModels = [...models]
    .filter(
      (m) =>
        m.request_count >= 20 &&
        1 - (m.error_count || 0) / m.request_count < OVERVIEW_SUCCESS_TARGET &&
        !seenModels.has(m.name),
    )
    .sort(
      (a, b) =>
        (b.error_count || 0) / b.request_count -
        (a.error_count || 0) / a.request_count,
    );

  for (const m of weakModels) {
    const rate = 1 - (m.error_count || 0) / m.request_count;
    items.push({
      key: `model-${m.name}`,
      name: m.name,
      detail: t(`${ns}.requestsAndFailures`, {
        requests: formatCount(m.request_count),
        errors: formatCount(m.error_count || 0),
        defaultValue: '{{requests}} 次请求 · {{errors}} 次失败',
      }),
      metric: formatRatio(rate),
      tone: rate < 0.95 ? 'alert' : 'warn',
      to: '/console/model-ops',
      mono: true,
    });
  }

  return items.slice(0, 5);
}

/** Builds executive KPI sub-lines with zero placeholder `—` dashes. */
export function buildPlatformKpiSublines(
  {
    totals,
    growth,
    topups,
  }: {
    totals: OverviewTotals | undefined;
    growth: OverviewGrowth | undefined;
    topups: OverviewData['topups'] | undefined;
  },
  t: TranslateFn = defaultTranslate,
) {
  const revenue = totals?.amount_microusd ?? 0;
  const cost = totals?.upstream_cost_microusd ?? 0;
  const margin = totals?.gross_margin_microusd ?? 0;
  const missingCost = Math.max(
    0,
    (totals?.request_count ?? 0) - (totals?.cost_sample_count ?? 0),
  );

  const marginBase =
    revenue > 0
      ? t(`${ns}.marginNote`, {
          ratio: `${((margin / revenue) * 100).toFixed(1)}%`,
          cost: formatMicroUsdCompact(cost),
          defaultValue: '毛利率 {{ratio}} · 成本 {{cost}}',
        })
      : `${t(`${ns}.cost`, { defaultValue: '上游成本' })} ${formatMicroUsdCompact(cost)}`;
  const marginSub =
    missingCost > 0
      ? `${marginBase}${t(`${ns}.missingCost`, {
          count: formatCount(missingCost),
          defaultValue: ' · {{count}} 次缺成本',
        })}`
      : marginBase;

  const serviceParts: string[] = [
    (totals?.error_count ?? 0) > 0
      ? t(`${ns}.requestsAndFailures`, {
          requests: formatCount(totals?.request_count ?? 0),
          errors: formatCount(totals?.error_count),
          defaultValue: '{{requests}} 次请求 · {{errors}} 次失败',
        })
      : t(`${ns}.requestsSub`, {
          count: formatCount(totals?.request_count ?? 0),
          defaultValue: '{{count}} 次请求',
        }),
  ];
  if (totals?.p95_ttft_ms && totals.p95_ttft_ms > 0) {
    serviceParts.push(
      `${t(`${ns}.p95ttft`, { defaultValue: 'P95 首字' })} ${(
        totals.p95_ttft_ms / 1000
      ).toFixed(2)}s`,
    );
  }

  const newUsers = growth?.new_users ?? 0;
  const paid = growth?.paid ?? 0;
  const growthParts: string[] = [
    `${t(`${ns}.newUsers`, { defaultValue: '新注册' })} ${formatCount(newUsers)}`,
  ];
  if (paid > 0) {
    growthParts.push(
      t(`${ns}.paidShortCount`, {
        count: formatCount(paid),
        defaultValue: '首充 {{count}}',
      }),
    );
  } else if ((topups?.count ?? 0) > 0) {
    growthParts.push(
      t(`${ns}.topupsShortCount`, {
        count: formatCount(topups?.count),
        defaultValue: '充值 {{count}} 笔',
      }),
    );
  }
  if (growth?.churn_applicable) {
    const net =
      newUsers + (growth.returned_users ?? 0) - (growth.churned_users ?? 0);
    growthParts.push(
      `${t(`${ns}.net`, { defaultValue: '净增' })} ${net >= 0 ? '+' : ''}${net}`,
    );
  } else if ((growth?.total_customers ?? 0) > 0) {
    growthParts.push(
      t(`${ns}.totalCustomers`, {
        count: formatCount(growth?.total_customers),
        defaultValue: '共 {{count}} 个用户',
      }),
    );
  }

  return {
    marginSub,
    serviceSub: serviceParts.join(' · '),
    growthSub: growthParts.join(' · '),
  };
}

export function relativeTime(unix: number, locale: string): string {
  const diff = Date.now() / 1000 - unix;
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
  if (diff < 3600)
    return rtf.format(-Math.max(1, Math.round(diff / 60)), 'minute');
  if (diff < 86400) return rtf.format(-Math.round(diff / 3600), 'hour');
  return new Intl.DateTimeFormat(locale, {
    month: 'numeric',
    day: 'numeric',
  }).format(new Date(unix * 1000));
}

import { useQuery } from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  bucketLabels,
  useOverviewQuery,
  type OverviewRange,
} from './overview-api';
import {
  comparisonLabel,
  Delta,
  Metric,
  NeedsLine,
  OverviewHeader,
  type NeedItem,
} from './overview-parts';
import {
  buildPlatformKpiSublines,
  buildPlatformWatchlist,
  OVERVIEW_SUCCESS_TARGET,
} from './overviewModel';
import {
  AgentBadge,
  agentName,
  isKnownAgent,
} from '../../../components/ui/agent-badge';
import { ComboChart } from '../../../components/ui/combo-chart';
import {
  formatCount,
  formatMicroUsd,
  formatMicroUsdCompact,
  formatRatio,
  splitDuration,
} from '../../../components/ui/metric-format';
import { PendingBlock, PendingValue } from '../../../components/ui/pending';
import { Select } from '../../../components/ui/select';
import { API, isRoot } from '../../../helpers';
import { useChannelHealthQuery } from '../../../hooks/health/useChannelHealthQuery';
import { unwrap } from '../../../lib/api-error';
import './overview.css';

const ns = 'page.console.overview';
const COCKPIT_CHART_HEIGHT = 230;

type Props = {
  range: OverviewRange;
  onRange: (range: OverviewRange) => void;
  onExport: (format: 'csv' | 'xlsx') => void;
};

type PlatformSummary = {
  total_customers?: number;
  low_balance_active?: number;
};

export default function PlatformOverview({ range, onRange, onExport }: Props) {
  const { t, i18n } = useTranslation();
  const query = useOverviewQuery('platform', range);
  const data = query.data;
  const pending = query.isPending;
  const health = useChannelHealthQuery('24h');
  const summary = useQuery<PlatformSummary>({
    queryKey: ['organizations-summary'],
    queryFn: async () =>
      unwrap<PlatformSummary>(
        await API.get('/api/user/summary', { skipErrorHandler: true }),
      ),
    staleTime: 60_000,
  });

  const { labels, titles, partial } = useMemo(
    () => bucketLabels(data, i18n.language),
    [data, i18n.language],
  );
  const nowLabel = t(`${ns}.now`, { defaultValue: '现在' });
  const axisLabels = labels.map((label) =>
    label === 'now' ? nowLabel : label,
  );
  const compare = comparisonLabel(range, t);
  const bucketWord =
    data?.bucket === 'hour'
      ? t(`${ns}.bucket.hour`, { defaultValue: '按小时' })
      : data?.bucket === 'week'
        ? t(`${ns}.bucket.week`, { defaultValue: '按周' })
        : t(`${ns}.bucket.day`, { defaultValue: '按天' });

  const totals = data?.totals;
  const previous = data?.previous;
  const growth = data?.growth;
  const successRate = totals?.success_rate ?? null;
  const revenue = totals?.amount_microusd ?? 0;
  const margin = totals?.gross_margin_microusd ?? 0;
  const series = data?.series || [];

  const needs: NeedItem[] = [];
  const weakModels = (data?.models || []).filter(
    (m) =>
      m.request_count >= 20 &&
      1 - (m.error_count || 0) / m.request_count < OVERVIEW_SUCCESS_TARGET,
  );
  if (weakModels.length) {
    const worst = weakModels.reduce((a, b) =>
      (a.error_count || 0) / a.request_count >
      (b.error_count || 0) / b.request_count
        ? a
        : b,
    );
    needs.push({
      key: 'models',
      to: '/console/health',
      content: t(`${ns}.needs.model`, {
        defaultValue: '{{model}} 成功率 {{rate}}，低于 99%',
        model: worst.name,
        rate: formatRatio(1 - (worst.error_count || 0) / worst.request_count),
      }),
    });
  }
  const issues = health.data?.summary?.active_issues ?? 0;
  if (issues > 0) {
    needs.push({
      key: 'channels',
      to: '/console/health',
      content: t(`${ns}.needs.channels`, {
        defaultValue: '{{count}} 个通道需要关注',
        count: issues,
      }),
    });
  }
  const lowBalance = summary.data?.low_balance_active ?? 0;
  if (lowBalance > 0) {
    needs.push({
      key: 'balance',
      to: '/console/organizations?filter=low_balance_active',
      content: t(`${ns}.needs.lowBalance`, {
        defaultValue: '{{count}} 个低余额用户仍在消费',
        count: lowBalance,
      }),
    });
  }
  const drafts = useAnnouncementDraftCount(isRoot());
  if ((drafts.data ?? 0) > 0) {
    needs.push({
      key: 'drafts',
      to: '/console/ops/notices',
      content: t(`${ns}.needs.drafts`, {
        defaultValue: '{{count}} 条公告待发布',
        count: drafts.data,
      }),
    });
  }

  const watchlist = useMemo(
    () =>
      buildPlatformWatchlist(
        {
          providers: health.data?.providers,
          rows: health.data?.rows,
          models: data?.models,
        },
        t,
      ),
    [data?.models, health.data?.providers, health.data?.rows, t],
  );

  const sublines = useMemo(
    () =>
      buildPlatformKpiSublines(
        {
          totals,
          growth,
          topups: data?.topups,
        },
        t,
      ),
    [data?.topups, growth, t, totals],
  );

  const newUsers = growth?.new_users ?? 0;
  const activated = growth?.activated ?? 0;
  const paid = growth?.paid ?? 0;
  const activeUsers = growth?.active_users ?? totals?.active_users ?? 0;
  const pct = (part: number) =>
    newUsers > 0 ? `${Math.round((part / newUsers) * 100)}%` : '';

  const clients = data?.clients || [];
  const clientTotal = clients.reduce((sum, c) => sum + c.request_count, 0);
  const topUsers = (data?.users || []).slice(0, 4);
  const topHealthyModels = (data?.models || [])
    .filter((m) => m.request_count > 0)
    .slice(0, 4);

  const totalWatchCount = Math.max(issues, watchlist.length);

  return (
    <div className='ov-page'>
      <OverviewHeader
        title={t(`${ns}.platformTitle`, { defaultValue: '平台总览' })}
        range={range}
        onRange={onRange}
        onExport={onExport}
        syncedLabel={
          query.dataUpdatedAt
            ? t(`${ns}.synced`, { defaultValue: '同步于 刚刚' })
            : undefined
        }
      />
      <NeedsLine items={needs} />

      <section
        className='ov-panel ov-kpis ov-kpis--platform ov-groups'
        aria-label={t(`${ns}.metrics`, { defaultValue: '关键指标' })}
      >
        <div className='ov-kpi'>
          <span className='ov-label'>
            {t(`${ns}.revenue`, { defaultValue: '收入' })}
          </span>
          <strong className='ov-money'>
            <Metric pending={pending} chars={8}>
              {formatMicroUsd(revenue)}
            </Metric>
          </strong>
          {pending ? (
            <PendingValue chars={12} />
          ) : (
            <Delta
              current={revenue}
              previous={previous?.amount_microusd ?? 0}
              label={compare}
            />
          )}
        </div>

        <div className='ov-kpi'>
          <span className='ov-label'>
            {t(`${ns}.margin`, { defaultValue: '毛利' })}
          </span>
          <strong className='ov-money'>
            <Metric pending={pending} chars={8}>
              {formatMicroUsd(margin)}
            </Metric>
          </strong>
          <span className='ov-note'>
            <Metric pending={pending} chars={16}>
              {sublines.marginSub}
            </Metric>
          </span>
        </div>

        <div className='ov-kpi'>
          <span className='ov-label'>
            {t(`${ns}.successRate`, { defaultValue: '成功率' })}
          </span>
          <strong
            className='ov-kv'
            data-alert={
              successRate !== null && successRate < OVERVIEW_SUCCESS_TARGET
                ? ''
                : undefined
            }
          >
            <Metric pending={pending} chars={6}>
              {successRate !== null
                ? formatRatio(successRate)
                : t(`${ns}.zeroRequestsShort`, { defaultValue: '0 次' })}
            </Metric>
          </strong>
          <span className='ov-note'>
            <Metric pending={pending} chars={14}>
              {sublines.serviceSub}
            </Metric>
          </span>
        </div>

        <div className='ov-kpi'>
          <span className='ov-label'>
            {t(`${ns}.activeUsers`, { defaultValue: '活跃用户' })}
          </span>
          <strong className='ov-kv'>
            <Metric pending={pending} chars={4}>
              {formatCount(activeUsers)}
            </Metric>
          </strong>
          <span className='ov-note'>
            <Metric pending={pending} chars={14}>
              {sublines.growthSub}
            </Metric>
          </span>
        </div>

        <div className='ov-kpi'>
          <span className='ov-label'>
            {t(`${ns}.opsWatchLabel`, { defaultValue: '通道与低余额预警' })}
          </span>
          <strong
            className='ov-kv'
            data-alert={totalWatchCount > 0 ? '' : undefined}
          >
            <Metric pending={pending && health.isPending} chars={5}>
              {totalWatchCount > 0
                ? t(`${ns}.watchIssueCount`, {
                    count: totalWatchCount,
                    defaultValue: '{{count}} 项待关注',
                  })
                : t(`${ns}.allHealthyShort`, { defaultValue: '运行正常' })}
            </Metric>
          </strong>
          <span className='ov-note'>
            <Metric pending={pending && summary.isPending} chars={12}>
              {lowBalance > 0 ? (
                <Link
                  className='ov-warn'
                  style={{ textDecoration: 'none' }}
                  to='/console/organizations?filter=low_balance_active'
                >
                  {t(`${ns}.lowBalanceWatchSub`, {
                    count: lowBalance,
                    defaultValue: '● {{count}} 户低余额活跃预警',
                  })}
                </Link>
              ) : (data?.topups?.count ?? 0) > 0 ? (
                t(`${ns}.topupsFoot`, {
                  defaultValue: '本期到账 {{amount}} · {{count}} 笔',
                  amount: formatMicroUsd(data?.topups?.amount_microusd ?? 0),
                  count: data?.topups?.count ?? 0,
                })
              ) : (
                t(`${ns}.zeroLowBalanceSub`, {
                  defaultValue: '无低余额断流预警',
                })
              )}
            </Metric>
          </span>
        </div>
      </section>

      <div className='ov-cockpit'>
        {/* Quadrant 1: Revenue, Cost & Gross Margin Trend */}
        <section className='ov-panel ov-chart-panel'>
          <div className='ov-chart-panel__title'>
            <strong>
              {t(`${ns}.chart.money`, { defaultValue: '收入、成本与毛利' })}
            </strong>
            <span className='ov-label'>
              {bucketWord} · USD
              {(data?.topups?.count ?? 0) > 0
                ? ` · ${t(`${ns}.topupsFoot`, {
                    defaultValue: '本期到账 {{amount}} · {{count}} 笔',
                    amount: formatMicroUsd(data?.topups?.amount_microusd ?? 0),
                    count: data?.topups?.count ?? 0,
                  })}`
                : ''}
            </span>
          </div>
          {pending ? (
            <PendingBlock height={COCKPIT_CHART_HEIGHT} />
          ) : (
            <ComboChart
              ariaLabel={t(`${ns}.chart.money`, {
                defaultValue: '收入、成本与毛利',
              })}
              labels={axisLabels}
              titles={titles}
              partial={partial}
              height={COCKPIT_CHART_HEIGHT}
              bars={[
                {
                  key: 'req',
                  label: t(`${ns}.legend.requests`, { defaultValue: '请求' }),
                  values: series.map((b) => b.request_count),
                  tone: 'pale',
                },
              ]}
              lines={[
                {
                  key: 'rev',
                  label: t(`${ns}.revenue`, { defaultValue: '收入' }),
                  values: series.map((b) => b.amount_microusd),
                  tone: 'ink',
                  area: true,
                },
                {
                  key: 'cost',
                  label: t(`${ns}.cost`, { defaultValue: '上游成本' }),
                  values: series.map((b) => b.upstream_cost_microusd ?? 0),
                  tone: 'grey',
                  dashed: true,
                },
              ]}
              left={{ format: (v) => formatCount(v) }}
              right={{ format: (v) => formatMicroUsdCompact(v) }}
              tooltip={(i) => {
                const b = series[i];
                const rev = b?.amount_microusd ?? 0;
                const m = b?.gross_margin_microusd ?? 0;
                return [
                  {
                    label: t(`${ns}.revenue`, { defaultValue: '收入' }),
                    value: formatMicroUsd(rev),
                    tone: 'ink',
                  },
                  {
                    label: t(`${ns}.cost`, { defaultValue: '上游成本' }),
                    value: formatMicroUsd(b?.upstream_cost_microusd ?? 0),
                    tone: 'grey',
                  },
                  {
                    label: t(`${ns}.margin`, { defaultValue: '毛利' }),
                    value: `${formatMicroUsd(m)} · ${rev > 0 ? ((m / rev) * 100).toFixed(1) : '0'}%`,
                    tone: 'ink',
                  },
                  {
                    label: t(`${ns}.legend.requests`, { defaultValue: '请求' }),
                    value: formatCount(b?.request_count),
                    tone: 'pale',
                  },
                ];
              }}
            />
          )}
        </section>

        {/* Quadrant 2: Actionable Channel & Model Watchlist */}
        <section className='ov-panel ov-cockpit-card'>
          <div className='ov-chart-panel__title'>
            <strong>
              {watchlist.length > 0
                ? t(`${ns}.watchlistTitleCount`, {
                    count: watchlist.length,
                    defaultValue: '需要关注的通道与模型 ({{count}})',
                  })
                : t(`${ns}.watchlistTitle`, {
                    defaultValue: '通道与模型健康度',
                  })}
            </strong>
            <span className='ov-cockpit-links'>
              <Link className='ov-link' to='/console/health'>
                {t(`${ns}.channelHealth`, { defaultValue: '模型状态 →' })}
              </Link>
              <Link className='ov-link' to='/console/model-ops'>
                {t(`${ns}.modelOpsLink`, { defaultValue: '模型运营 →' })}
              </Link>
            </span>
          </div>

          {pending && health.isPending ? (
            <PendingBlock height={176} />
          ) : watchlist.length > 0 ? (
            <div className='ov-watch-list'>
              {watchlist.map((item) => (
                <Link
                  key={item.key}
                  className='ov-watch-item'
                  data-tone={item.tone}
                  to={item.to}
                >
                  <div className='ov-watch-item__copy'>
                    <strong className={item.mono ? 'lz-mono' : undefined}>
                      {item.name}
                    </strong>
                    <span className='ov-note'>{item.detail}</span>
                  </div>
                  <span
                    className={`lz-mono ${item.tone === 'alert' ? 'ov-alert' : 'ov-warn'}`}
                  >
                    {item.metric}
                  </span>
                </Link>
              ))}
            </div>
          ) : (
            <div className='ov-healthy-box'>
              <div className='ov-healthy-sla'>
                <span className='ov-healthy-sla__dot' aria-hidden='true' />
                <div>
                  <strong>
                    {t(`${ns}.slaHealthyTitle`, {
                      defaultValue: '所有在服通道与主力模型运行正常',
                    })}
                  </strong>
                  <span className='ov-note'>
                    {[
                      health.data?.summary?.stable_success_rate !== undefined
                        ? `稳定线路 ${formatRatio(health.data.summary.stable_success_rate)}`
                        : '',
                      health.data?.summary?.eco_success_rate !== undefined
                        ? `Agent 专线 ${formatRatio(health.data.summary.eco_success_rate)}`
                        : '',
                      health.data?.summary?.provider_count
                        ? `${formatCount(health.data.summary.provider_count)} 个供货源在线`
                        : '',
                    ]
                      .filter(Boolean)
                      .join(' · ') ||
                      t(`${ns}.slaHealthySub`, {
                        defaultValue: '无低于 99% 目标的模型或降级通道',
                      })}
                  </span>
                </div>
              </div>
              {topHealthyModels.length > 0 ? (
                <div className='ov-mini-table'>
                  {topHealthyModels.map((m) => {
                    const rate = 1 - (m.error_count || 0) / m.request_count;
                    return (
                      <div key={m.name} className='ov-mini-row'>
                        <span className='lz-mono'>{m.name}</span>
                        <span className='ov-note'>
                          {formatCount(m.request_count)} 次
                        </span>
                        <span className='lz-mono'>{formatRatio(rate)}</span>
                      </div>
                    );
                  })}
                </div>
              ) : null}
            </div>
          )}
        </section>

        {/* Quadrant 3: Low-Balance Active Customers + Conversion Funnel */}
        <section className='ov-panel ov-cockpit-card'>
          <div className='ov-chart-panel__title'>
            <strong>
              {t(`${ns}.customersAndFunnel`, {
                defaultValue: '活跃客户消耗与新客转化',
              })}
            </strong>
            <Link
              className='ov-link'
              to={
                lowBalance > 0
                  ? '/console/organizations?filter=low_balance_active'
                  : '/console/organizations'
              }
            >
              {lowBalance > 0
                ? t(`${ns}.lowBalanceLink`, {
                    count: lowBalance,
                    defaultValue: '低余额预警 ({{count}}) →',
                  })
                : t(`${ns}.accounts`, { defaultValue: '客户账户 →' })}
            </Link>
          </div>

          {lowBalance > 0 ? (
            <Link
              className='ov-lowbal-callout'
              to='/console/organizations?filter=low_balance_active'
            >
              <span>
                {t(`${ns}.needs.lowBalance`, {
                  defaultValue: '{{count}} 个低余额用户仍在消费',
                  count: lowBalance,
                })}
              </span>
              <span className='ov-warn'>
                {t(`${ns}.inspectAction`, { defaultValue: '去处理 →' })}
              </span>
            </Link>
          ) : null}

          {pending ? (
            <PendingBlock height={96} />
          ) : topUsers.length > 0 ? (
            <div className='ov-mini-table'>
              {topUsers.map((u) => {
                const ratio =
                  u.amount_microusd > 0
                    ? (u.gross_margin_microusd || 0) / u.amount_microusd
                    : 0;
                return (
                  <div key={`${u.id}-${u.name}`} className='ov-mini-row'>
                    <Link
                      className='ov-list__link'
                      to={`/console/usage?${new URLSearchParams({
                        scope: 'platform',
                        owner: u.name || '',
                        ...(u.id ? { user_id: String(u.id) } : {}),
                      })}`}
                    >
                      {u.name}
                    </Link>
                    <span className='lz-mono'>
                      {formatMicroUsd(u.amount_microusd)}
                    </span>
                    <span
                      className={`lz-mono ${ratio < 0.2 && u.amount_microusd > 0 ? 'ov-warn' : ''}`}
                    >
                      {u.amount_microusd > 0
                        ? `${(ratio * 100).toFixed(1)}%`
                        : '0%'}
                    </span>
                  </div>
                );
              })}
            </div>
          ) : null}

          <div className='ov-compact-funnel'>
            <div className='ov-compact-funnel__head'>
              <span className='ov-label'>
                {t(`${ns}.funnel`, { defaultValue: '新用户漏斗' })}
              </span>
              <span className='ov-note'>
                {t(`${ns}.funnelNote`, {
                  defaultValue: '本期注册的 {{count}} 人',
                  count: formatCount(newUsers),
                })}
              </span>
            </div>
            {[
              {
                key: 'reg',
                label: t(`${ns}.funnel.registered`, { defaultValue: '注册' }),
                value: newUsers,
                pctText: '',
                fill: 'color-mix(in srgb, var(--lazu-ink) 22%, transparent)',
              },
              {
                key: 'act',
                label: t(`${ns}.funnel.firstRequest`, {
                  defaultValue: '发出第一个请求',
                }),
                value: activated,
                pctText: pct(activated),
                fill: 'color-mix(in srgb, var(--lazu-ink) 40%, transparent)',
              },
              {
                key: 'paid',
                label: t(`${ns}.funnel.firstTopup`, {
                  defaultValue: '第一次充值',
                }),
                value: paid,
                pctText: pct(paid),
                fill: 'var(--lazu-chart-1)',
              },
            ].map((step) => (
              <div key={step.key} className='ov-funnel__step'>
                <div className='ov-funnel__head'>
                  <span>{step.label}</span>
                  <span>
                    <strong>
                      {pending ? (
                        <PendingValue chars={3} />
                      ) : (
                        formatCount(step.value)
                      )}
                    </strong>
                    {step.pctText ? (
                      <span className='ov-label'> {step.pctText}</span>
                    ) : null}
                  </span>
                </div>
                <div className='ov-funnel__track'>
                  <div
                    className='ov-funnel__fill'
                    style={{
                      width: `${newUsers > 0 ? (step.value / newUsers) * 100 : 0}%`,
                      background: step.fill,
                    }}
                  />
                </div>
              </div>
            ))}
          </div>

          <SignupSources
            sources={growth?.sources}
            campaigns={growth?.campaigns}
            landingPages={growth?.landing_pages}
            pending={pending}
          />
        </section>

        {/* Quadrant 4: Client Agent Ecosystem Share */}
        <section className='ov-panel ov-cockpit-card'>
          <div className='ov-chart-panel__title'>
            <strong>
              {t(`${ns}.clientEcosystem`, {
                defaultValue: '活跃客户端生态占比',
              })}
            </strong>
            <span className='ov-note'>
              {data?.sessions
                ? [
                    `${formatCount(data.sessions.sessions)} 会话`,
                    data.sessions.median_amount_microusd != null
                      ? `中位 ${formatMicroUsd(data.sessions.median_amount_microusd)}`
                      : '',
                    data.sessions.median_duration_seconds != null
                      ? formatSessionDuration(
                          data.sessions.median_duration_seconds,
                          t,
                        )
                      : '',
                  ]
                    .filter(Boolean)
                    .join(' · ')
                : t(`${ns}.clientsNote`, {
                    defaultValue: '用户在用哪些 agent',
                  })}
            </span>
          </div>

          {pending ? (
            <PendingBlock height={176} />
          ) : clients.length === 0 ? (
            <p className='ov-empty-line'>
              {t(`${ns}.clientsEmpty`, {
                defaultValue: '这段时间没有请求。有请求后，这里按客户端拆开。',
              })}
            </p>
          ) : (
            <div className='ov-agent-bars'>
              {clients.slice(0, 6).map((client) => {
                const known = isKnownAgent(client.client);
                const share =
                  clientTotal > 0 ? client.request_count / clientTotal : 0;
                const name =
                  client.client === 'unknown'
                    ? t(`${ns}.unknownClient`, {
                        defaultValue: '通用 SDK / 其他客户端',
                      })
                    : agentName(client.client);
                return (
                  <div key={client.client} className='ov-agent-bar'>
                    <div className='ov-agent-bar__head'>
                      <span className='ov-clients__name'>
                        <AgentBadge
                          client={
                            client.client === 'unknown' ? '' : client.client
                          }
                          ungrouped={!known}
                        />
                        <span>{name}</span>
                      </span>
                      <span className='ov-agent-bar__nums'>
                        <span className='ov-note'>
                          {formatCount(client.users)} 人 ·{' '}
                          {formatMicroUsdCompact(client.amount_microusd)}
                        </span>
                        <strong className='lz-mono'>
                          {Math.round(share * 100)}%
                        </strong>
                      </span>
                    </div>
                    <div className='ov-share__track'>
                      <i
                        className='ov-share__fill'
                        data-faint={known ? undefined : ''}
                        style={{ width: `${share * 100}%` }}
                      />
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

type SignupBreakdown = Array<{
  source: string;
  users: number;
  activated: number;
  paid: number;
}>;

function SignupSources({
  sources,
  campaigns,
  landingPages,
  pending,
}: {
  sources?: SignupBreakdown;
  campaigns?: SignupBreakdown;
  landingPages?: SignupBreakdown;
  pending: boolean;
}) {
  const { t } = useTranslation();
  const [dimension, setDimension] = useState('source');
  const rows =
    dimension === 'campaign'
      ? campaigns
      : dimension === 'landing'
        ? landingPages
        : sources;
  if (pending || !sources || !sources.length) return null;
  return (
    <div className='ov-sources-box'>
      <div className='ov-compact-funnel__head'>
        <span className='ov-label'>
          {t(`${ns}.sources`, { defaultValue: '注册来源' })}
        </span>
        <Select
          size='sm'
          aria-label={t(`${ns}.attributionDimension`, {
            defaultValue: '归因维度',
          })}
          value={dimension}
          onValueChange={setDimension}
          options={[
            {
              value: 'source',
              label: t(`${ns}.sources`, { defaultValue: '注册来源' }),
            },
            {
              value: 'campaign',
              label: t(`${ns}.campaigns`, { defaultValue: '推广活动' }),
            },
            {
              value: 'landing',
              label: t(`${ns}.landingPages`, { defaultValue: '首次落地页' }),
            },
          ]}
        />
      </div>
      {(rows ?? []).slice(0, 4).map((s) => (
        <div key={s.source} className='ov-source-row'>
          <span className='lz-mono' style={{ fontSize: 12 }}>
            {s.source ||
              (dimension === 'source'
                ? t(`${ns}.sourceDirect`, { defaultValue: '直接访问' })
                : t(`${ns}.attributionMissing`, { defaultValue: '未记录' }))}
          </span>
          <span className='ov-note' style={{ color: 'var(--lazu-ink)' }}>
            {t(`${ns}.sourceRow`, {
              defaultValue: '{{users}} 人 · 调用 {{a}} · 充值 {{p}}',
              users: s.users,
              a: s.activated,
              p: s.paid,
            })}
          </span>
        </div>
      ))}
    </div>
  );
}

type AnnouncementPage = {
  items?: { published_at?: string | null; unpublished_at?: string | null }[];
  next_before_id?: number;
};

function useAnnouncementDraftCount(enabled: boolean) {
  return useQuery({
    enabled,
    queryKey: ['announcements', 'draft-count'],
    queryFn: async () => {
      let before: number | undefined;
      let count = 0;
      do {
        const data =
          unwrap<AnnouncementPage | undefined>(
            await API.get('/api/admin/announcements', {
              params: { limit: 50, before_id: before },
              skipErrorHandler: true,
            }),
          ) ?? {};
        count += (data.items || []).filter(
          (item) => !item.published_at && !item.unpublished_at,
        ).length;
        const next = data.next_before_id;
        if (next && before && next >= before) break;
        before = next && next > 0 ? next : undefined;
      } while (before);
      return count;
    },
    staleTime: 60_000,
  });
}

function formatSessionDuration(
  seconds: number,
  t: (key: string, options?: Record<string, unknown>) => string,
): string {
  if (seconds < 60)
    return t(`${ns}.seconds`, {
      defaultValue: '{{count}} 秒',
      count: Math.round(seconds),
    });
  if (seconds < 3600)
    return t(`${ns}.minutes`, {
      defaultValue: '{{count}} 分',
      count: Math.round(seconds / 60),
    });
  const { hours, minutes } = splitDuration(seconds);
  return t(`${ns}.hoursMinutes`, {
    defaultValue: '{{h}} 小时 {{m}} 分',
    h: hours,
    m: minutes,
  });
}

import { useQuery } from '@tanstack/react-query';
import React, { useMemo, useState } from 'react';
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
  ListCard,
  Metric,
  NeedsLine,
  OverviewHeader,
  type NeedItem,
} from './overview-parts';
import {
  Select,
  AgentBadge,
  agentName,
  formatCount,
  formatMicroUsd,
  formatMicroUsdCompact,
  formatRatio,
  isKnownAgent,
  PendingBlock,
  PendingValue,
  splitDuration,
} from '../../../components/ui';
import { ComboChart } from '../../../components/ui/combo-chart';
import { API } from '../../../helpers/api';
import { isRoot } from '../../../helpers/utils';
import { useChannelHealthQuery } from '../../../hooks/health/useChannelHealthQuery';
import { unwrap } from '../../../lib/api-error';
import './overview.css';

const ns = 'page.console.overview';
const SUCCESS_TARGET = 0.99;
const CHART_HEIGHT = 290;

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
  const health = useChannelHealthQuery('7d');
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
  const cost = totals?.upstream_cost_microusd ?? 0;
  const margin = totals?.gross_margin_microusd ?? 0;
  const missingCost = Math.max(
    0,
    (totals?.request_count ?? 0) - (totals?.cost_sample_count ?? 0),
  );

  const series = data?.series || [];
  const rateSeries = series.map((b) =>
    b.outcome_count > 0
      ? (b.outcome_count - b.error_count) / b.outcome_count
      : null,
  );
  const rateValues = rateSeries.filter((v): v is number => v !== null);
  const rateFloor = rateValues.length
    ? Math.min(0.97, Math.floor((Math.min(...rateValues) - 0.004) * 100) / 100)
    : 0.97;

  const needs: NeedItem[] = [];
  const weakModels = (data?.models || []).filter(
    (m) =>
      m.request_count >= 20 &&
      m.request_count > 0 &&
      1 - (m.error_count || 0) / m.request_count < SUCCESS_TARGET,
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

  const newUsers = growth?.new_users ?? 0;
  const activated = growth?.activated ?? 0;
  const paid = growth?.paid ?? 0;
  const net =
    newUsers + (growth?.returned_users ?? 0) - (growth?.churned_users ?? 0);
  // No sign-ups means no ratio to state: say nothing rather than print a
  // dash next to the zero it already explains.
  const pct = (part: number) =>
    newUsers > 0 ? `${Math.round((part / newUsers) * 100)}%` : '';

  const clients = data?.clients || [];
  const clientTotal = clients.reduce((sum, c) => sum + c.request_count, 0);

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
        className='ov-panel ov-groups'
        aria-label={t(`${ns}.metrics`, { defaultValue: '关键指标' })}
      >
        <div className='ov-group'>
          <span className='ov-group__name'>
            {t(`${ns}.group.service`, { defaultValue: '服务质量' })}
          </span>
          <div className='ov-group__cells'>
            <div className='ov-cell'>
              <span className='ov-label'>
                {t(`${ns}.successRate`, { defaultValue: '成功率' })}
              </span>
              <strong
                className='ov-kv ov-kv--hero'
                data-alert={
                  successRate !== null && successRate < SUCCESS_TARGET
                    ? ''
                    : undefined
                }
              >
                <Metric pending={pending} chars={6}>
                  {formatRatio(successRate)}
                </Metric>
              </strong>
              <span className='ov-note'>
                <Metric pending={pending} chars={14}>
                  {t(`${ns}.requestsAndFailures`, {
                    defaultValue: '{{requests}} 次请求 · {{errors}} 次失败',
                    requests: formatCount(totals?.request_count),
                    errors: formatCount(totals?.error_count),
                  })}
                </Metric>
              </span>
            </div>
            <div className='ov-cell'>
              <span className='ov-label'>
                {t(`${ns}.p95ttft`, { defaultValue: 'P95 首字' })}
              </span>
              <strong className='ov-kv'>
                <Metric pending={pending}>
                  {totals?.p95_ttft_ms
                    ? `${(totals.p95_ttft_ms / 1000).toFixed(2)}s`
                    : '—'}
                </Metric>
              </strong>
              <span className='ov-note'>
                {t(`${ns}.streamText`, { defaultValue: '流式文本' })}
              </span>
            </div>
          </div>
        </div>
        <div className='ov-group'>
          <span className='ov-group__name'>
            {t(`${ns}.group.revenue`, { defaultValue: '收入' })}
          </span>
          <div className='ov-group__cells'>
            <div className='ov-cell'>
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
            <div className='ov-cell'>
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
                  {t(`${ns}.marginNote`, {
                    defaultValue: '毛利率 {{ratio}} · 成本 {{cost}}',
                    ratio:
                      revenue > 0
                        ? `${((margin / revenue) * 100).toFixed(1)}%`
                        : '—',
                    cost: formatMicroUsdCompact(cost),
                  })}
                  {missingCost > 0
                    ? t(`${ns}.missingCost`, {
                        defaultValue: ' · {{count}} 次缺成本',
                        count: formatCount(missingCost),
                      })
                    : ''}
                </Metric>
              </span>
            </div>
          </div>
        </div>
        <div className='ov-group'>
          <span className='ov-group__name'>
            {t(`${ns}.group.growth`, { defaultValue: '用户增长' })}
          </span>
          <div className='ov-group__cells'>
            <div className='ov-cell'>
              <span className='ov-label'>
                {t(`${ns}.newUsers`, { defaultValue: '新注册' })}
              </span>
              <strong className='ov-kv'>
                <Metric pending={pending}>{formatCount(newUsers)}</Metric>
              </strong>
              <span className='ov-note'>
                <Metric pending={pending} chars={14}>
                  {t(`${ns}.activationNote`, {
                    defaultValue: '首次调用 {{a}} · 首充 {{p}}',
                    a: pct(activated),
                    p: pct(paid),
                  })}
                </Metric>
              </span>
            </div>
            <div className='ov-cell'>
              <span className='ov-label'>
                {t(`${ns}.activeUsers`, { defaultValue: '活跃用户' })}
              </span>
              <strong className='ov-kv'>
                <Metric pending={pending}>
                  {formatCount(growth?.active_users)}
                </Metric>
              </strong>
              <span className='ov-note'>
                <Metric pending={pending} chars={10}>
                  {t(`${ns}.totalCustomers`, {
                    defaultValue: '共 {{count}} 个用户',
                    count: formatCount(growth?.total_customers),
                  })}
                </Metric>
              </span>
            </div>
            <div className='ov-cell'>
              <span className='ov-label'>
                {t(`${ns}.churned`, { defaultValue: '流失' })}
              </span>
              <strong className='ov-kv'>
                <Metric pending={pending}>
                  {growth?.churn_applicable
                    ? formatCount(growth.churned_users)
                    : '—'}
                </Metric>
              </strong>
              <span className='ov-note'>
                {growth && !growth.churn_applicable
                  ? t(`${ns}.churnNA`, {
                      defaultValue: '连续 7 天无请求才算，需选 7 天以上',
                    })
                  : t(`${ns}.churnNote`, {
                      defaultValue: '连续 7 天没有请求 · 回流 {{count}}',
                      count: formatCount(growth?.returned_users),
                    })}
              </span>
            </div>
          </div>
        </div>
      </section>

      <div className='ov-grid-2'>
        <section className='ov-panel ov-chart-panel'>
          <div className='ov-chart-panel__title'>
            <strong>
              {t(`${ns}.chart.success`, { defaultValue: '成功率' })}
            </strong>
            <span className='ov-label'>
              {bucketWord} ·{' '}
              {t(`${ns}.chart.successNote`, { defaultValue: '请求量作背景' })}
            </span>
          </div>
          {pending ? (
            <PendingBlock height={CHART_HEIGHT} />
          ) : (
            <ComboChart
              ariaLabel={t(`${ns}.chart.success`, { defaultValue: '成功率' })}
              labels={axisLabels}
              titles={titles}
              partial={partial}
              height={CHART_HEIGHT}
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
                  key: 'rate',
                  label: t(`${ns}.successRate`, { defaultValue: '成功率' }),
                  values: rateSeries,
                  tone: 'ink',
                },
              ]}
              left={{ format: (v) => formatCount(v) }}
              right={{
                format: (v) => `${(v * 100).toFixed(1)}%`,
                min: rateFloor,
                max: 1,
              }}
              threshold={{
                value: SUCCESS_TARGET,
                label: t(`${ns}.target`, { defaultValue: '99% 目标' }),
              }}
              alertPoints={rateSeries.map(
                (v) => v !== null && v < SUCCESS_TARGET,
              )}
              extraLegend={[
                {
                  label: t(`${ns}.target`, { defaultValue: '99% 目标' }),
                  tone: 'danger',
                  kind: 'dash',
                },
              ]}
              tooltip={(i) => {
                const b = series[i];
                const rate = rateSeries[i];
                return [
                  {
                    label: t(`${ns}.successRate`, { defaultValue: '成功率' }),
                    value: formatRatio(rate),
                    tone: 'ink',
                    alert: rate !== null && rate < SUCCESS_TARGET,
                  },
                  {
                    label: t(`${ns}.legend.requests`, { defaultValue: '请求' }),
                    value: formatCount(b?.request_count),
                    tone: 'pale',
                  },
                  {
                    label: t(`${ns}.legend.failures`, { defaultValue: '失败' }),
                    value: formatCount(b?.error_count),
                    tone: 'danger',
                  },
                ];
              }}
            />
          )}
        </section>
        <section className='ov-panel ov-chart-panel'>
          <div className='ov-chart-panel__title'>
            <strong>
              {t(`${ns}.chart.money`, { defaultValue: '收入、成本与毛利' })}
            </strong>
            <span className='ov-label'>{bucketWord} · USD</span>
          </div>
          {pending ? (
            <PendingBlock height={CHART_HEIGHT} />
          ) : (
            <ComboChart
              ariaLabel={t(`${ns}.chart.money`, {
                defaultValue: '收入、成本与毛利',
              })}
              labels={axisLabels}
              titles={titles}
              partial={partial}
              height={CHART_HEIGHT}
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
      </div>

      <div className='ov-grid-growth'>
        <section className='ov-panel ov-chart-panel'>
          <div className='ov-chart-panel__title'>
            <strong>
              {t(`${ns}.chart.growth`, { defaultValue: '用户增长' })}
            </strong>
            <span className='ov-label'>
              {bucketWord} · {t(`${ns}.people`, { defaultValue: '人' })}
            </span>
          </div>
          {pending ? (
            <PendingBlock height={CHART_HEIGHT} />
          ) : (
            <ComboChart
              ariaLabel={t(`${ns}.chart.growth`, { defaultValue: '用户增长' })}
              labels={axisLabels}
              titles={titles}
              partial={partial}
              height={CHART_HEIGHT}
              bars={[
                {
                  key: 'new',
                  label: t(`${ns}.newUsers`, { defaultValue: '新注册' }),
                  values: series.map((b) => b.new_users ?? 0),
                  tone: 'ink',
                },
              ]}
              lines={[
                {
                  key: 'active',
                  label: t(`${ns}.activeUsers`, { defaultValue: '活跃用户' }),
                  values: series.map((b) => b.active_users ?? 0),
                  tone: 'grey',
                },
              ]}
              left={{ format: (v) => formatCount(v) }}
              right={{ format: (v) => formatCount(v) }}
              tooltip={(i) => {
                const b = series[i];
                return [
                  {
                    label: t(`${ns}.newUsers`, { defaultValue: '新注册' }),
                    value: formatCount(b?.new_users ?? 0),
                    tone: 'ink',
                  },
                  {
                    label: t(`${ns}.returned`, { defaultValue: '回流' }),
                    value: formatCount(b?.returned_users ?? 0),
                    tone: 'grey',
                  },
                  {
                    label: t(`${ns}.churned`, { defaultValue: '流失' }),
                    value:
                      b?.churned_users === undefined
                        ? '—'
                        : formatCount(b.churned_users),
                    tone: 'danger',
                  },
                  {
                    label: t(`${ns}.activeUsers`, { defaultValue: '活跃用户' }),
                    value: formatCount(b?.active_users ?? 0),
                    tone: 'grey',
                  },
                ];
              }}
            />
          )}
        </section>
        <section className='ov-panel ov-funnel'>
          <div className='ov-chart-panel__title'>
            <strong style={{ fontSize: 15 }}>
              {t(`${ns}.funnel`, { defaultValue: '新用户漏斗' })}
            </strong>
            <span className='ov-label'>
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
              pct: '',
              fill: 'color-mix(in srgb, var(--lazu-ink) 22%, transparent)',
            },
            {
              key: 'act',
              label: t(`${ns}.funnel.firstRequest`, {
                defaultValue: '发出第一个请求',
              }),
              value: activated,
              pct: pct(activated),
              fill: 'color-mix(in srgb, var(--lazu-ink) 40%, transparent)',
            },
            {
              key: 'paid',
              label: t(`${ns}.funnel.firstTopup`, {
                defaultValue: '第一次充值',
              }),
              value: paid,
              pct: pct(paid),
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
                  <span className='ov-label'> {step.pct}</span>
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
          <div className='ov-funnel__facts'>
            <div>
              <span className='ov-label'>
                {t(`${ns}.returned`, { defaultValue: '回流' })}
              </span>
              <strong>
                {pending ? (
                  <PendingValue chars={3} />
                ) : (
                  formatCount(growth?.returned_users)
                )}
              </strong>
            </div>
            <div>
              <span className='ov-label'>
                {t(`${ns}.churned`, { defaultValue: '流失' })}
              </span>
              <strong>
                {pending ? (
                  <PendingValue chars={3} />
                ) : growth?.churn_applicable ? (
                  formatCount(growth.churned_users)
                ) : (
                  '—'
                )}
              </strong>
            </div>
            <div>
              <span className='ov-label'>
                {t(`${ns}.net`, { defaultValue: '净增' })}
              </span>
              <strong
                className={net < 0 ? 'ov-alert' : undefined}
                style={net >= 0 ? { color: 'var(--lazu-success)' } : undefined}
              >
                {pending ? (
                  <PendingValue chars={3} />
                ) : growth?.churn_applicable ? (
                  `${net >= 0 ? '+' : ''}${net}`
                ) : (
                  '—'
                )}
              </strong>
            </div>
          </div>
          <SignupSources
            sources={growth?.sources}
            campaigns={growth?.campaigns}
            landingPages={growth?.landing_pages}
            pending={pending}
          />
        </section>
      </div>

      <div>
        <section className='ov-panel' style={{ padding: '16px 20px' }}>
          <div className='ov-chart-panel__title'>
            <strong style={{ fontSize: 14.5 }}>
              {t(`${ns}.clients`, { defaultValue: '客户端' })}
            </strong>
            <span className='ov-note'>
              {t(`${ns}.clientsNote`, { defaultValue: '用户在用哪些 agent' })}
            </span>
          </div>
          <div className='ov-clients__stats'>
            <span>
              <span className='ov-note'>
                {t(`${ns}.sessions`, { defaultValue: '会话' })}
              </span>
              <strong>
                {pending ? (
                  <PendingValue chars={3} />
                ) : (
                  formatCount(data?.sessions?.sessions)
                )}
              </strong>
            </span>
            <span>
              <span className='ov-note'>
                {t(`${ns}.sessionCost`, { defaultValue: '每个会话花费' })}
              </span>
              <strong>
                {pending ? (
                  <PendingValue chars={4} />
                ) : (
                  formatMicroUsd(data?.sessions?.median_amount_microusd)
                )}
              </strong>
            </span>
            <span>
              <span className='ov-note'>
                {t(`${ns}.sessionDuration`, { defaultValue: '每个会话时长' })}
              </span>
              <strong>
                {pending ? (
                  <PendingValue chars={4} />
                ) : data?.sessions?.median_duration_seconds != null ? (
                  formatSessionDuration(
                    data.sessions.median_duration_seconds,
                    t,
                  )
                ) : (
                  '—'
                )}
              </strong>
            </span>
            <span>
              <span className='ov-note'>
                {t(`${ns}.subagentShare`, { defaultValue: '用了 subagent' })}
              </span>
              <strong>
                {pending ? (
                  <PendingValue chars={3} />
                ) : (
                  formatRatio(data?.sessions?.subagent_share, 0)
                )}
              </strong>
            </span>
          </div>
          {!pending && clients.length === 0 ? (
            <p className='ov-empty-line'>
              {t(`${ns}.clientsEmpty`, {
                defaultValue: '这段时间没有请求。有请求后，这里按客户端拆开。',
              })}
            </p>
          ) : (
            <>
              <div className='ov-clients__row ov-clients__row--head'>
                <span>{t(`${ns}.client`, { defaultValue: '客户端' })}</span>
                <span>{t(`${ns}.users`, { defaultValue: '用户' })}</span>
                <span>{t(`${ns}.sessions`, { defaultValue: '会话' })}</span>
                <span>Tokens</span>
                <span>{t(`${ns}.spend`, { defaultValue: '花费' })}</span>
                <span>
                  {t(`${ns}.requestShare`, { defaultValue: '占请求' })}
                </span>
              </div>
              {pending
                ? Array.from({ length: 5 }, (_, i) => (
                    <div key={i} className='ov-clients__row'>
                      <PendingValue chars={12} />
                      <PendingValue chars={2} />
                      <PendingValue chars={2} />
                      <PendingValue chars={4} />
                      <PendingValue chars={4} />
                      <PendingValue chars={8} />
                    </div>
                  ))
                : clients.slice(0, 7).map((client) => {
                    const known = isKnownAgent(client.client);
                    const share =
                      clientTotal > 0 ? client.request_count / clientTotal : 0;
                    const name =
                      client.client === 'unknown'
                        ? t(`${ns}.unknownClient`, {
                            defaultValue: '没认出来的程序',
                          })
                        : known
                          ? agentName(client.client)
                          : client.client;
                    return (
                      <div key={client.client} className='ov-clients__row'>
                        <span className='ov-clients__name'>
                          <AgentBadge
                            client={
                              client.client === 'unknown' ? '' : client.client
                            }
                            ungrouped={!known}
                          />
                          <span
                            style={
                              known
                                ? undefined
                                : { color: 'var(--lazu-text-muted)' }
                            }
                          >
                            {name}
                          </span>
                        </span>
                        <span>{formatCount(client.users)}</span>
                        <span>
                          {known && client.sessions > 0
                            ? formatCount(client.sessions)
                            : '—'}
                        </span>
                        <span style={{ fontWeight: 600 }}>
                          {formatCount(client.tokens)}
                        </span>
                        <span className='lz-mono'>
                          {formatMicroUsdCompact(client.amount_microusd)}
                        </span>
                        <span className='ov-share'>
                          <span className='ov-share__track'>
                            <i
                              className='ov-share__fill'
                              data-faint={known ? undefined : ''}
                              style={{ width: `${share * 100}%` }}
                            />
                          </span>
                          <span
                            className='ov-note'
                            style={{ width: 36, textAlign: 'right' }}
                          >
                            {Math.round(share * 100)}%
                          </span>
                        </span>
                      </div>
                    );
                  })}
              <p className='ov-note' style={{ margin: '10px 0 0' }}>
                {t(`${ns}.clientsFoot`, {
                  defaultValue:
                    '虚线标记的是只带通用 SDK 标识的请求（openai-python、httpx 等），多半是用户自己写的程序，不算 agent。',
                })}
              </p>
            </>
          )}
        </section>
      </div>

      <div className='ov-grid-3'>
        <ListCard
          title={t(`${ns}.modelSuccess`, { defaultValue: '模型成功率' })}
          link={{
            to: '/console/health',
            label: t(`${ns}.channelHealth`, { defaultValue: '通道健康 →' }),
          }}
          columns={[
            t(`${ns}.model`, { defaultValue: '模型' }),
            t(`${ns}.legend.requests`, { defaultValue: '请求' }),
            t(`${ns}.successRate`, { defaultValue: '成功率' }),
          ]}
          pending={pending}
          empty={t(`${ns}.noTraffic`, { defaultValue: '这段时间没有请求' })}
          rows={[...(data?.models || [])]
            .filter((m) => m.request_count > 0)
            .sort(
              (a, b) =>
                (b.error_count || 0) / b.request_count -
                (a.error_count || 0) / a.request_count,
            )
            .slice(0, 6)
            .map((m) => {
              const rate = 1 - (m.error_count || 0) / m.request_count;
              return {
                key: m.name,
                name: m.name,
                mono: true,
                b: formatCount(m.request_count),
                c: formatRatio(rate),
                tone: rate < SUCCESS_TARGET ? ('alert' as const) : undefined,
              };
            })}
          foot={t(`${ns}.modelSuccessFoot`, {
            defaultValue: '按成功率从低到高，低于 99% 标红',
          })}
        />
        <ListCard
          title={t(`${ns}.userMargin`, { defaultValue: '用户毛利' })}
          link={{
            to: '/console/organizations',
            label: t(`${ns}.allUsers`, { defaultValue: '全部用户 →' }),
          }}
          columns={[
            t(`${ns}.user`, { defaultValue: '用户' }),
            t(`${ns}.revenue`, { defaultValue: '收入' }),
            t(`${ns}.marginRate`, { defaultValue: '毛利率' }),
          ]}
          pending={pending}
          empty={t(`${ns}.noTraffic`, { defaultValue: '这段时间没有请求' })}
          rows={(data?.users || []).slice(0, 6).map((u) => {
            const ratio =
              u.amount_microusd > 0
                ? (u.gross_margin_microusd || 0) / u.amount_microusd
                : null;
            return {
              key: `${u.id}-${u.name}`,
              name: (
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
              ),
              b: formatMicroUsd(u.amount_microusd),
              c: ratio === null ? '—' : `${(ratio * 100).toFixed(1)}%`,
              tone:
                ratio !== null && ratio < 0.2 ? ('warn' as const) : undefined,
            };
          })}
          foot={t(`${ns}.userMarginFoot`, {
            defaultValue: '毛利率低于 20% 标黄',
          })}
        />
        <ListCard
          title={t(`${ns}.topups`, { defaultValue: '充值到账' })}
          link={{
            to: '/console/organizations',
            label: t(`${ns}.accounts`, { defaultValue: '客户账户 →' }),
          }}
          columns={[
            t(`${ns}.user`, { defaultValue: '用户' }),
            t(`${ns}.amount`, { defaultValue: '金额' }),
            t(`${ns}.time`, { defaultValue: '时间' }),
          ]}
          pending={pending}
          empty={t(`${ns}.topupsEmpty`, {
            defaultValue: '这段时间没有到账的充值',
          })}
          rows={(data?.topups?.recent || []).map((topup, i) => ({
            key: `${topup.username}-${topup.completed_at}-${i}`,
            name: topup.username || '—',
            b: formatMicroUsd(topup.amount_microusd),
            c: (
              <span className='ov-note'>
                {relativeTime(topup.completed_at, i18n.language)}
              </span>
            ),
          }))}
          foot={
            data?.topups
              ? t(`${ns}.topupsFoot`, {
                  defaultValue: '本期到账 {{amount}} · {{count}} 笔',
                  amount: formatMicroUsd(data.topups.amount_microusd),
                  count: data.topups.count,
                })
              : undefined
          }
        />
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
  if (pending) return <PendingBlock height={64} />;
  if (!sources || !sources.length) {
    return (
      <div className='ov-slot'>
        {t(`${ns}.sourcesEmpty`, {
          defaultValue:
            '本期新用户还没有记录到注册来源。推广链接带上 utm_source / utm_campaign，这里就会按来源拆开看转化。',
        })}
      </div>
    );
  }
  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 6,
        borderTop: '1px solid var(--lazu-border)',
        paddingTop: 12,
      }}
    >
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
      {(rows ?? []).map((s) => (
        <div
          key={s.source}
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            fontSize: 12.5,
          }}
        >
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

/** Unpublished announcement drafts; only root can manage announcements. */
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

function relativeTime(unix: number, locale: string): string {
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

export { relativeTime };

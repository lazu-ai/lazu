import React, { useMemo } from 'react';
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
import { relativeTime } from './PlatformOverview';
import { AgentBadge } from '../../../components/ui/agent-badge';
import { ComboChart } from '../../../components/ui/combo-chart';
import {
  formatCount,
  formatMicroUsd,
  formatMicroUsdCompact,
  formatRatio,
} from '../../../components/ui/metric-format';
import { PendingBlock, PendingValue } from '../../../components/ui/pending';
import { useAgentSessionsQuery } from '../../../hooks/usage-logs/useAgentSessions';
import './overview.css';

const ns = 'page.console.overview';
const SUCCESS_TARGET = 0.99;
const CHART_HEIGHT = 300;
const RUNWAY_WARN_DAYS = 7;

type Props = {
  range: OverviewRange;
  onRange: (range: OverviewRange) => void;
  onExport: (format: 'csv' | 'xlsx') => void;
  /** Wallet balance in microUSD; undefined while it is still loading. */
  balance: number | undefined;
  /** Links to apps beside the console, shown before the range picker. */
  appLinks?: { href: string; label: string }[];
  canInspectUsage: boolean;
  canManageKeys: boolean;
};

function rangeDays(range: OverviewRange, start?: number, end?: number): number {
  if (start && end && end > start) return Math.max(1, (end - start) / 86400);
  return { '24h': 1, '7d': 7, '30d': 30, '90d': 90, custom: 1 }[range.preset];
}

export default function WorkspaceOverview({
  range,
  onRange,
  onExport,
  balance,
  appLinks = [],
  canInspectUsage,
  canManageKeys,
}: Props) {
  const { t, i18n } = useTranslation();
  const query = useOverviewQuery('self', range);
  const data = query.data;
  const pending = query.isPending;
  const sessions = useAgentSessionsQuery({
    admin: false,
    page: 1,
    pageSize: 4,
    range: range.preset === 'custom' ? '90d' : range.preset,
    startTimestamp: range.preset === 'custom' ? range.start : undefined,
    enabled: canInspectUsage,
  });
  const { labels, titles, partial } = useMemo(
    () => bucketLabels(data, i18n.language),
    [data, i18n.language],
  );
  const nowLabel = t(`${ns}.now`, { defaultValue: '现在' });
  const axisLabels = labels.map((label) =>
    label === 'now' ? nowLabel : label,
  );

  const totals = data?.totals;
  const spend = totals?.amount_microusd ?? 0;
  const tokens =
    (totals?.prompt_tokens ?? 0) + (totals?.completion_tokens ?? 0);
  const requests = totals?.request_count ?? 0;
  const successRate = totals?.success_rate ?? null;
  const cacheRatio =
    totals && totals.prompt_tokens > 0
      ? totals.cached_tokens / totals.prompt_tokens
      : null;
  const perDay = spend / rangeDays(range, data?.start, data?.end);
  const runwayDays =
    balance !== undefined && perDay > 0 ? balance / perDay : null;
  const series = data?.series || [];

  const needs: NeedItem[] = [];
  if (runwayDays !== null && runwayDays < RUNWAY_WARN_DAYS) {
    needs.push({
      key: 'runway',
      to: '/console/wallet',
      content: t(`${ns}.needs.runway`, {
        defaultValue: '按最近的用量，余额约 {{days}} 天内用完',
        days: Math.max(0, Math.round(runwayDays)),
      }),
    });
  }
  if (
    canInspectUsage &&
    successRate !== null &&
    successRate < SUCCESS_TARGET &&
    (totals?.outcome_count ?? 0) >= 20
  ) {
    needs.push({
      key: 'failures',
      to: `/console/usage?${new URLSearchParams({
        type: '5',
        start_timestamp: String(data?.start ?? 0),
        end_timestamp: String(data?.end ?? 0),
      })}`,
      content: t(`${ns}.needs.failures`, {
        defaultValue: '成功率 {{rate}}，有 {{count}} 次请求失败',
        rate: formatRatio(successRate),
        count: formatCount(totals?.error_count),
      }),
    });
  }

  const chartTitle =
    data?.bucket === 'hour'
      ? t(`${ns}.chart.hourly`, { defaultValue: '每小时用量' })
      : data?.bucket === 'week'
        ? t(`${ns}.chart.weekly`, { defaultValue: '每周用量' })
        : t(`${ns}.chart.daily`, { defaultValue: '每日用量' });

  return (
    <div className='ov-page'>
      <OverviewHeader
        title={t(`${ns}.workspaceTitle`, { defaultValue: '总览' })}
        range={range}
        onRange={onRange}
        onExport={onExport}
        syncedLabel={
          query.dataUpdatedAt
            ? t(`${ns}.synced`, { defaultValue: '同步于 刚刚' })
            : undefined
        }
        leading={
          appLinks.length > 0
            ? appLinks.map((link) => (
                <a
                  className='ov-link'
                  href={link.href}
                  key={link.href}
                  style={{ fontSize: 13, marginRight: 6 }}
                >
                  {link.label} ↗
                </a>
              ))
            : null
        }
      />
      <NeedsLine items={needs} />

      <section
        className='ov-panel ov-kpis'
        style={{ gridTemplateColumns: '1fr 1fr 1fr 1fr 1.25fr' }}
      >
        <div className='ov-kpi'>
          <span className='ov-label'>
            {t(`${ns}.spend`, { defaultValue: '消耗' })}
          </span>
          <strong className='ov-money'>
            <Metric pending={pending} chars={7}>
              {formatMicroUsd(spend)}
            </Metric>
          </strong>
          {pending ? (
            <PendingValue chars={12} />
          ) : (
            <Delta
              current={spend}
              previous={data?.previous?.amount_microusd ?? 0}
              label={comparisonLabel(range, t)}
            />
          )}
        </div>
        <div className='ov-kpi'>
          <span className='ov-label'>
            {t(`${ns}.balance`, { defaultValue: '可用余额' })}
          </span>
          <strong className='ov-money'>
            <Metric pending={balance === undefined} chars={7}>
              {formatMicroUsd(balance)}
            </Metric>
          </strong>
          <span className='ov-note'>
            <Link className='ov-link' to='/console/wallet'>
              {t(`${ns}.topup`, { defaultValue: '充值 →' })}
            </Link>
          </span>
        </div>
        <div className='ov-kpi'>
          <span className='ov-label'>
            {t(`${ns}.runway`, { defaultValue: '预计还能用' })}
          </span>
          <strong
            className='ov-kv'
            data-alert={
              runwayDays !== null && runwayDays < RUNWAY_WARN_DAYS
                ? ''
                : undefined
            }
          >
            <Metric pending={pending || balance === undefined}>
              {runwayDays === null
                ? '—'
                : runwayDays >= 365
                  ? t(`${ns}.runwayYear`, { defaultValue: '一年以上' })
                  : t(`${ns}.runwayDays`, {
                      defaultValue: '约 {{count}} 天',
                      count: Math.max(0, Math.round(runwayDays)),
                    })}
            </Metric>
          </strong>
          <span className='ov-note'>
            <Metric pending={pending} chars={10}>
              {perDay > 0
                ? t(`${ns}.perDay`, {
                    defaultValue: '按日均 {{amount}}',
                    amount: formatMicroUsd(perDay),
                  })
                : t(`${ns}.noSpend`, { defaultValue: '这段时间没有消耗' })}
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
            <Metric pending={pending} chars={8}>
              {t(`${ns}.failures`, {
                defaultValue: '{{count}} 次失败',
                count: formatCount(totals?.error_count),
              })}
            </Metric>
          </span>
        </div>
        <div className='ov-kpi'>
          <span className='ov-label'>
            {t(`${ns}.tokensRequests`, { defaultValue: 'Tokens · 请求' })}
          </span>
          <span className='ov-pair'>
            <strong className='ov-kv'>
              <Metric pending={pending}>{formatCount(tokens)}</Metric>
            </strong>
            <span className='ov-kv--small'>
              <Metric pending={pending} chars={4}>
                {t(`${ns}.requestsCount`, {
                  defaultValue: '{{count}} 次',
                  count: formatCount(requests),
                })}
              </Metric>
            </span>
          </span>
          <span className='ov-note'>
            <Metric pending={pending} chars={18}>
              {t(`${ns}.perRequest`, {
                defaultValue: '平均每次 {{per}} tokens · 缓存命中 {{cache}}',
                per: requests > 0 ? formatCount(tokens / requests) : '—',
                cache: formatRatio(cacheRatio, 0),
              })}
            </Metric>
          </span>
        </div>
      </section>

      <section
        className='ov-panel ov-chart-panel'
        style={{ padding: '16px 24px 10px' }}
      >
        <div className='ov-chart-panel__title'>
          <strong>{chartTitle}</strong>
          <span className='ov-label'>
            {t(`${ns}.chart.usageNote`, {
              defaultValue: 'Tokens 在左轴，消耗在右轴',
            })}
          </span>
        </div>
        {pending ? (
          <PendingBlock height={CHART_HEIGHT} />
        ) : (
          <ComboChart
            ariaLabel={chartTitle}
            labels={axisLabels}
            titles={titles}
            partial={partial}
            height={CHART_HEIGHT}
            bars={[
              {
                key: 'tokens',
                label: 'Tokens',
                values: series.map(
                  (b) => b.prompt_tokens + b.completion_tokens,
                ),
                tone: 'ink',
              },
            ]}
            lines={[
              {
                key: 'spend',
                label: t(`${ns}.spend`, { defaultValue: '消耗' }),
                values: series.map((b) => b.amount_microusd),
                tone: 'grey',
                dashed: true,
              },
            ]}
            left={{ format: (v) => formatCount(v) }}
            right={{ format: (v) => formatMicroUsdCompact(v) }}
            tooltip={(i) => {
              const b = series[i];
              const failed = b?.error_count ?? 0;
              return [
                {
                  label: 'Tokens',
                  value: formatCount(
                    (b?.prompt_tokens ?? 0) + (b?.completion_tokens ?? 0),
                  ),
                  tone: 'ink',
                },
                {
                  label: t(`${ns}.spend`, { defaultValue: '消耗' }),
                  value: formatMicroUsd(b?.amount_microusd ?? 0),
                  tone: 'grey',
                },
                {
                  label: t(`${ns}.legend.requests`, { defaultValue: '请求' }),
                  value: formatCount(b?.request_count),
                  tone: 'pale',
                },
                ...(failed > 0
                  ? [
                      {
                        label: t(`${ns}.legend.failures`, {
                          defaultValue: '失败',
                        }),
                        value: formatCount(failed),
                        tone: 'danger' as const,
                        alert: true,
                      },
                    ]
                  : []),
              ];
            }}
          />
        )}
      </section>

      <div className='ov-grid-3'>
        <ListCard
          title={t(`${ns}.byModel`, { defaultValue: '按模型' })}
          link={
            canInspectUsage
              ? {
                  to: '/console/usage',
                  label: t(`${ns}.usageLink`, { defaultValue: '用量与日志 →' }),
                }
              : undefined
          }
          columns={[
            t(`${ns}.model`, { defaultValue: '模型' }),
            'Tokens',
            t(`${ns}.spend`, { defaultValue: '消耗' }),
          ]}
          pending={pending}
          empty={t(`${ns}.noTraffic`, { defaultValue: '这段时间没有请求' })}
          rows={(data?.models || []).slice(0, 4).map((m) => ({
            key: m.name,
            name: m.name,
            mono: true,
            sub: t(`${ns}.requestsSub`, {
              defaultValue: '{{count}} 次请求',
              count: formatCount(m.request_count),
            }),
            b: formatCount(m.prompt_tokens + m.completion_tokens),
            c: formatMicroUsd(m.amount_microusd),
          }))}
        />
        <ListCard
          title={t(`${ns}.byKey`, { defaultValue: '按 Key' })}
          link={
            canManageKeys
              ? { to: '/console/token', label: 'API Keys →' }
              : undefined
          }
          columns={[
            'Key',
            'Tokens',
            t(`${ns}.spend`, { defaultValue: '消耗' }),
          ]}
          pending={pending}
          empty={t(`${ns}.noTraffic`, { defaultValue: '这段时间没有请求' })}
          rows={(data?.keys || []).slice(0, 4).map((k) => ({
            key: `${k.id}-${k.name}`,
            name:
              k.name || t(`${ns}.deletedKey`, { defaultValue: '已删除的 Key' }),
            sub: [
              t(`${ns}.requestsSub`, {
                defaultValue: '{{count}} 次请求',
                count: formatCount(k.request_count),
              }),
              k.last_used_at ? relativeTime(k.last_used_at, i18n.language) : '',
            ]
              .filter(Boolean)
              .join(' · '),
            b: formatCount(k.prompt_tokens + k.completion_tokens),
            c: formatMicroUsd(k.amount_microusd),
          }))}
        />
        <RecentSessions
          pending={canInspectUsage && sessions.isPending}
          items={sessions.data?.items || []}
          visible={canInspectUsage}
        />
      </div>
    </div>
  );
}

type SessionItem = NonNullable<
  ReturnType<typeof useAgentSessionsQuery>['data']
>['items'][number];

const LIVE_WINDOW_SECONDS = 5 * 60;

function RecentSessions({
  pending,
  items,
  visible,
}: {
  pending: boolean;
  items: SessionItem[];
  visible: boolean;
}) {
  const { t, i18n } = useTranslation();
  if (!visible) return null;
  const now = Date.now() / 1000;
  return (
    <section className='ov-panel ov-list'>
      <div className='ov-list__head'>
        <strong>
          {t(`${ns}.recentSessions`, { defaultValue: '最近的会话' })}
        </strong>
        <Link
          className='ov-link'
          to='/console/usage/sessions'
          style={{ fontSize: 12.5 }}
        >
          {t(`${ns}.allSessions`, { defaultValue: '全部会话 →' })}
        </Link>
      </div>
      {pending ? (
        Array.from({ length: 4 }, (_, i) => (
          <div key={i} className='ov-session'>
            <PendingValue chars={2} />
            <span className='ov-session__meta'>
              <PendingValue chars={18} />
              <PendingValue chars={12} />
            </span>
            <span className='ov-session__right'>
              <PendingValue chars={5} />
              <PendingValue chars={4} />
            </span>
          </div>
        ))
      ) : items.length ? (
        items.map((item) => {
          const live = now - item.last_at < LIVE_WINDOW_SECONDS;
          const tokens =
            item.tokens ?? item.prompt_tokens + item.completion_tokens;
          const title =
            item.title ||
            (item.source === 'token'
              ? t(`${ns}.sessionByKey`, {
                  defaultValue: '{{key}} 的请求',
                  key: item.token_name || 'Key',
                })
              : item.session_id);
          return (
            <div key={item.id} className='ov-session'>
              <AgentBadge
                client={item.client}
                ungrouped={item.source === 'token'}
              />
              <span className='ov-session__meta'>
                <Link
                  className='ov-session__title'
                  to={`/console/usage/sessions/${encodeURIComponent(item.id)}`}
                  title={title}
                >
                  {title}
                </Link>
                <span className='ov-note'>
                  {relativeTime(item.last_at, i18n.language)} ·{' '}
                  <strong style={{ fontWeight: 600, color: 'var(--lazu-ink)' }}>
                    {formatCount(tokens)}
                  </strong>{' '}
                  ·{' '}
                  {t(`${ns}.requestsCount`, {
                    defaultValue: '{{count}} 次',
                    count: formatCount(item.request_count),
                  })}
                </span>
              </span>
              <span className='ov-session__right'>
                <span className='lz-mono'>
                  {formatMicroUsd(item.amount_microusd)}
                </span>
                <span
                  style={{
                    fontSize: 11.5,
                    color:
                      item.error_count > 0
                        ? 'var(--lazu-danger)'
                        : live
                          ? 'var(--lazu-success)'
                          : 'var(--lazu-text-faint)',
                  }}
                >
                  {item.error_count > 0
                    ? t(`${ns}.sessionFailures`, {
                        defaultValue: '{{count}} 次失败',
                        count: item.error_count,
                      })
                    : live
                      ? t(`${ns}.sessionLive`, { defaultValue: '进行中' })
                      : t(`${ns}.sessionDone`, { defaultValue: '已结束' })}
                </span>
              </span>
            </div>
          );
        })
      ) : (
        <p className='ov-list__empty'>
          {t(`${ns}.sessionsEmpty`, {
            defaultValue:
              '这段时间没有会话。Claude Code、Codex 等 agent 发来的请求会按会话归在这里。',
          })}
        </p>
      )}
    </section>
  );
}

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
  computeWorkspaceRunwayDays,
  OVERVIEW_RUNWAY_WARN_DAYS,
  OVERVIEW_SUCCESS_TARGET,
  relativeTime,
} from './overviewModel';
import { AgentBadge } from '../../../components/ui/agent-badge';
import { Button } from '../../../components/ui/button';
import { ComboChart } from '../../../components/ui/combo-chart';
import {
  formatCount,
  formatMicroUsd,
  formatMicroUsdCompact,
  formatRatio,
} from '../../../components/ui/metric-format';
import { PendingBlock, PendingValue } from '../../../components/ui/pending';
import { notify } from '../../../components/ui/toast';
import { useAgentSessionsQuery } from '../../../hooks/usage-logs/useAgentSessions';
import './overview.css';

const ns = 'page.console.overview';
const COMPACT_CHART_HEIGHT = 200;

type Props = {
  range: OverviewRange;
  onRange: (range: OverviewRange) => void;
  onExport: (format: 'csv' | 'xlsx') => void;
  /** Wallet balance in microUSD; undefined while it is still loading. */
  balance: number | undefined;
  /** Gateway API Base URL ending in /v1. */
  apiBaseURL: string;
  /** Links to apps beside the console, shown before the range picker. */
  appLinks?: { href: string; label: string }[];
  canInspectUsage: boolean;
  canManageKeys: boolean;
  canViewModels: boolean;
  /** True when Dashboard already renders OnboardingTakeover above this view. */
  suppressZeroLaunchpad?: boolean;
};

export default function WorkspaceOverview({
  range,
  onRange,
  onExport,
  balance,
  apiBaseURL,
  appLinks = [],
  canInspectUsage,
  canManageKeys,
  canViewModels,
  suppressZeroLaunchpad = false,
}: Props) {
  const { t, i18n } = useTranslation();
  const [copiedEndpoint, setCopiedEndpoint] = useState(false);
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
      ? Math.min(1, totals.cached_tokens / totals.prompt_tokens)
      : null;
  const runwayDays = computeWorkspaceRunwayDays(
    balance,
    spend,
    range,
    data?.start,
    data?.end,
  );
  const series = data?.series || [];

  // Low-balance runway lives inside KPI Cell 1; NeedsLine stays strictly for
  // failure-rate alerts so runway warnings are never repeated twice.
  const needs: NeedItem[] = [];
  if (
    canInspectUsage &&
    successRate !== null &&
    successRate < OVERVIEW_SUCCESS_TARGET &&
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
      ? t(`${ns}.chart.hourlyModels`, {
          defaultValue: '每小时消耗趋势与主力模型',
        })
      : data?.bucket === 'week'
        ? t(`${ns}.chart.weeklyModels`, {
            defaultValue: '每周消耗趋势与主力模型',
          })
        : t(`${ns}.chart.dailyModels`, {
            defaultValue: '每日消耗趋势与主力模型',
          });

  const copyEndpoint = async () => {
    try {
      await navigator.clipboard.writeText(apiBaseURL);
      setCopiedEndpoint(true);
      notify.success(t('secret.copied', { defaultValue: '已复制' }));
      window.setTimeout(() => setCopiedEndpoint(false), 1800);
    } catch {
      /* clipboard blocked in non-secure context */
    }
  };

  const zeroTraffic = !pending && requests === 0;
  const topModels = (data?.models || []).slice(0, 3);
  const activeKeys = (data?.keys || []).slice(0, 3);

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

      <section className='ov-panel ov-kpis ov-kpis--workspace'>
        <div className='ov-kpi ov-kpi--balance'>
          <div className='ov-kpi__balance-copy'>
            <span className='ov-label'>
              {t(`${ns}.balance`, { defaultValue: '可用余额' })}
              {!pending && balance !== undefined && runwayDays !== null ? (
                runwayDays < OVERVIEW_RUNWAY_WARN_DAYS ? (
                  <>
                    {' · '}
                    <span className='ov-runway-inline' data-alert=''>
                      {t(`${ns}.runwayWarnInline`, {
                        days: Math.max(0, Math.round(runwayDays)),
                        defaultValue: '预计约 {{days}} 天用完',
                      })}
                    </span>
                  </>
                ) : runwayDays < 365 ? (
                  <>
                    {' · '}
                    <span className='ov-runway-inline'>
                      {t(`${ns}.runwayDays`, {
                        count: Math.round(runwayDays),
                        defaultValue: '约 {{count}} 天',
                      })}
                    </span>
                  </>
                ) : null
              ) : null}
            </span>
            <strong className='ov-money ov-money--hero'>
              <Metric pending={balance === undefined} chars={7}>
                {formatMicroUsd(balance)}
              </Metric>
            </strong>
          </div>
          <Button
            nativeButton={false}
            render={<Link to='/console/wallet' />}
            size='sm'
            variant='primary'
          >
            {t(`${ns}.topupNow`, { defaultValue: '立即充值' })}
          </Button>
        </div>

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
            {t(`${ns}.successAndRequests`, { defaultValue: '请求与成功率' })}
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
              {requests > 0
                ? formatRatio(successRate)
                : t(`${ns}.zeroRequestsShort`, { defaultValue: '0 次' })}
            </Metric>
          </strong>
          <span className='ov-note'>
            <Metric pending={pending} chars={10}>
              {requests > 0
                ? t(`${ns}.requestsAndFailures`, {
                    defaultValue: '{{requests}} 次请求 · {{errors}} 次失败',
                    requests: formatCount(requests),
                    errors: formatCount(totals?.error_count),
                  })
                : t(`${ns}.noTraffic`, { defaultValue: '这段时间没有请求' })}
            </Metric>
          </span>
        </div>

        <div className='ov-kpi'>
          <span className='ov-label'>
            {t(`${ns}.tokensAndCache`, { defaultValue: 'Tokens · 缓存命中' })}
          </span>
          <strong className='ov-kv'>
            <Metric pending={pending} chars={6}>
              {formatCount(tokens)}
            </Metric>
          </strong>
          <span className='ov-note'>
            <Metric pending={pending} chars={16}>
              {cacheRatio !== null
                ? t(`${ns}.cacheHitNote`, {
                    defaultValue: 'Prompt 缓存命中 {{cache}}',
                    cache: formatRatio(cacheRatio, 1),
                  })
                : t(`${ns}.requestsSub`, {
                    defaultValue: '{{count}} 次请求',
                    count: formatCount(requests),
                  })}
            </Metric>
          </span>
        </div>
      </section>

      {zeroTraffic ? (
        suppressZeroLaunchpad ? null : (
          <section className='ov-panel ov-zero-launchpad'>
            <div className='ov-zero-launchpad__head'>
              <span className='ov-zero-launchpad__eyebrow'>QUICKSTART</span>
              <h2 className='ov-zero-launchpad__title'>
                {t(`${ns}.zero.title`, {
                  defaultValue: '3 步跑通第一条 Agent 请求',
                })}
              </h2>
              <p className='ov-note'>
                {t(`${ns}.zero.subtitle`, {
                  defaultValue:
                    '发出首条请求后，这里会自动切换为实时消耗趋势与会话视图。',
                })}
              </p>
            </div>
            <div className='ov-zero-steps'>
              <div className='ov-zero-step'>
                <div>
                  <span className='ov-zero-step__tag'>
                    {balance !== undefined && balance > 0
                      ? t(`${ns}.zero.step1Ready`, {
                          defaultValue: '✓ 第 1 步 · 已就绪',
                        })
                      : t(`${ns}.zero.step1Tag`, {
                          defaultValue: '● 第 1 步',
                        })}
                  </span>
                  <strong className='ov-zero-step__title'>
                    {t(`${ns}.zero.step1Title`, {
                      defaultValue: '确认额度与充值',
                    })}
                  </strong>
                  <p className='ov-zero-step__desc'>
                    {balance !== undefined && balance > 0
                      ? t(`${ns}.zero.step1ReadyDesc`, {
                          balance: formatMicroUsd(balance),
                          defaultValue:
                            '当前可用余额 {{balance}}，按实际 Token 用量结算。',
                        })
                      : t(`${ns}.zero.step1Desc`, {
                          defaultValue:
                            '充值或确认账户可用额度，按实际消耗计费。',
                        })}
                  </p>
                </div>
                <Button
                  nativeButton={false}
                  render={<Link to='/console/wallet' />}
                  size='sm'
                  variant={balance && balance > 0 ? 'secondary' : 'primary'}
                >
                  {t(`${ns}.zero.step1Action`, { defaultValue: '去钱包查看' })}
                </Button>
              </div>

              <div className='ov-zero-step'>
                <div>
                  <span className='ov-zero-step__tag'>
                    {t(`${ns}.zero.step2Tag`, { defaultValue: '● 第 2 步' })}
                  </span>
                  <strong className='ov-zero-step__title'>
                    {t(`${ns}.zero.step2Title`, {
                      defaultValue: '创建 API Key',
                    })}
                  </strong>
                  <p className='ov-zero-step__desc'>
                    {t(`${ns}.zero.step2Desc`, {
                      defaultValue:
                        '支持独立设置每日预算、允许模型与思考深度上限。',
                    })}
                  </p>
                </div>
                {canManageKeys ? (
                  <Button
                    nativeButton={false}
                    render={<Link to='/console/token' />}
                    size='sm'
                    variant={balance && balance > 0 ? 'primary' : 'secondary'}
                  >
                    {t(`${ns}.zero.step2Action`, {
                      defaultValue: '创建 API Key',
                    })}
                  </Button>
                ) : null}
              </div>

              <div className='ov-zero-step'>
                <div>
                  <span className='ov-zero-step__tag'>
                    {t(`${ns}.zero.step3Tag`, { defaultValue: '○ 第 3 步' })}
                  </span>
                  <strong className='ov-zero-step__title'>
                    {t(`${ns}.zero.step3Title`, {
                      defaultValue: '接入 Claude Code / Cursor',
                    })}
                  </strong>
                  <p className='ov-zero-step__desc'>
                    {t(`${ns}.zero.step3Desc`, {
                      endpoint: apiBaseURL,
                      defaultValue:
                        '替换 Base URL 为 {{endpoint}} 即可发起首条请求。',
                    })}
                  </p>
                </div>
                <Button
                  nativeButton={false}
                  render={
                    <Link
                      to={canManageKeys ? '/console/token' : '/quickstart'}
                    />
                  }
                  size='sm'
                  variant='secondary'
                >
                  {t(`${ns}.zero.step3Action`, {
                    defaultValue: '打开接入向导',
                  })}
                </Button>
              </div>
            </div>
          </section>
        )
      ) : (
        <>
          <div className='ov-workspace-stage'>
            <section className='ov-panel ov-trend-card'>
              <div className='ov-chart-panel__title'>
                <strong>{chartTitle}</strong>
                {canInspectUsage ? (
                  <Link className='ov-link' to='/console/usage'>
                    {t(`${ns}.usageLink`, { defaultValue: '用量与日志 →' })}
                  </Link>
                ) : null}
              </div>
              {pending ? (
                <PendingBlock height={COMPACT_CHART_HEIGHT} />
              ) : (
                <ComboChart
                  ariaLabel={chartTitle}
                  labels={axisLabels}
                  titles={titles}
                  partial={partial}
                  height={COMPACT_CHART_HEIGHT}
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
                        label: t(`${ns}.legend.requests`, {
                          defaultValue: '请求',
                        }),
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

              <div className='ov-top-models'>
                {pending
                  ? Array.from({ length: 3 }, (_, i) => (
                      <div key={i} className='ov-top-model'>
                        <PendingValue chars={14} />
                        <PendingValue chars={12} />
                      </div>
                    ))
                  : topModels.length > 0
                    ? topModels.map((m) => {
                        const share =
                          spend > 0
                            ? Math.round((m.amount_microusd / spend) * 100)
                            : 0;
                        return (
                          <div key={m.name} className='ov-top-model'>
                            <strong className='lz-mono ov-top-model__name'>
                              {m.name}
                            </strong>
                            <span className='ov-note'>
                              {formatMicroUsd(m.amount_microusd)} ({share}%) ·{' '}
                              {t(`${ns}.requestsCount`, {
                                defaultValue: '{{count}} 次',
                                count: formatCount(m.request_count),
                              })}
                            </span>
                          </div>
                        );
                      })
                    : null}
              </div>
            </section>

            <section className='ov-panel ov-launchpad-card'>
              <div className='ov-chart-panel__title'>
                <strong>
                  {t(`${ns}.quickConnectTitle`, {
                    defaultValue: '快速接入与活跃 API Keys',
                  })}
                </strong>
                {canManageKeys ? (
                  <Link className='ov-link' to='/console/token'>
                    API Keys →
                  </Link>
                ) : null}
              </div>

              <div className='ov-endpoint-bar'>
                <span className='ov-endpoint-bar__url'>
                  <span className='ov-note'>
                    {t(`${ns}.endpointLabel`, { defaultValue: '接入端点' })}
                  </span>
                  <code className='lz-mono'>{apiBaseURL}</code>
                </span>
                <Button
                  size='sm'
                  type='button'
                  variant='secondary'
                  onClick={() => void copyEndpoint()}
                >
                  {copiedEndpoint
                    ? t('secret.copied', { defaultValue: '已复制' })
                    : t('secret.copy', { defaultValue: '复制' })}
                </Button>
              </div>

              <div className='ov-key-list'>
                {pending ? (
                  Array.from({ length: 2 }, (_, i) => (
                    <div key={i} className='ov-key-item'>
                      <PendingValue chars={14} />
                      <PendingValue chars={8} />
                    </div>
                  ))
                ) : activeKeys.length > 0 ? (
                  activeKeys.map((k) => (
                    <div key={`${k.id}-${k.name}`} className='ov-key-item'>
                      <div className='ov-key-item__meta'>
                        <strong>
                          {k.name ||
                            t(`${ns}.deletedKey`, {
                              defaultValue: '已删除的 Key',
                            })}
                        </strong>
                        <span className='ov-note'>
                          {[
                            t(`${ns}.requestsCount`, {
                              defaultValue: '{{count}} 次',
                              count: formatCount(k.request_count),
                            }),
                            k.last_used_at
                              ? relativeTime(k.last_used_at, i18n.language)
                              : '',
                          ]
                            .filter(Boolean)
                            .join(' · ')}
                        </span>
                      </div>
                      <span className='lz-mono ov-key-item__spend'>
                        {formatMicroUsd(k.amount_microusd)}
                      </span>
                    </div>
                  ))
                ) : (
                  <p className='ov-empty-line'>
                    {t(`${ns}.noKeyTraffic`, {
                      defaultValue: '当前时间范围内暂无 API Key 调用记录',
                    })}
                  </p>
                )}
              </div>

              {canManageKeys || canViewModels ? (
                <div className='ov-launchpad-actions'>
                  {canManageKeys ? (
                    <Button
                      nativeButton={false}
                      render={<Link to='/console/token' />}
                      size='sm'
                      variant='secondary'
                    >
                      {t(`${ns}.connectToolAction`, {
                        defaultValue: '接入工具与管理 Key',
                      })}
                    </Button>
                  ) : null}
                  {canViewModels ? (
                    <Button
                      nativeButton={false}
                      render={<Link to='/console/models' />}
                      size='sm'
                      variant='secondary'
                    >
                      {t(`${ns}.browseModelsAction`, {
                        defaultValue: '浏览模型目录',
                      })}
                    </Button>
                  ) : null}
                </div>
              ) : null}
            </section>
          </div>

          <RecentSessions
            pending={canInspectUsage && sessions.isPending}
            items={sessions.data?.items || []}
            visible={canInspectUsage}
          />
        </>
      )}
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
    <section className='ov-panel ov-sessions-strip'>
      <div className='ov-chart-panel__title'>
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
        <div className='ov-sessions-grid'>
          {Array.from({ length: 2 }, (_, i) => (
            <div key={i} className='ov-session-card'>
              <PendingValue chars={2} />
              <span className='ov-session__meta'>
                <PendingValue chars={18} />
                <PendingValue chars={12} />
              </span>
              <span className='ov-session__right'>
                <PendingValue chars={5} />
              </span>
            </div>
          ))}
        </div>
      ) : items.length ? (
        <div className='ov-sessions-grid'>
          {items.slice(0, 4).map((item) => {
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
              <div key={item.id} className='ov-session-card'>
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
                    <strong
                      style={{ fontWeight: 600, color: 'var(--lazu-ink)' }}
                    >
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
          })}
        </div>
      ) : (
        <p className='ov-empty-line'>
          {t(`${ns}.sessionsEmpty`, {
            defaultValue:
              '这段时间没有会话。Claude Code、Codex 等 agent 发来的请求会按会话归在这里。',
          })}
        </p>
      )}
    </section>
  );
}

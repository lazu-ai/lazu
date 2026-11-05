import {
  AlertTriangle,
  Columns3,
  MoreHorizontal,
  Pencil,
  Plus,
  Power,
  Search,
  ShieldCheck,
  Trash2,
  Zap,
} from 'lucide-react';
import { useMemo, useState } from 'react';
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom';
import { formatLatency, formatRate, healthTone } from './formatters';
import { foldBuckets, ModelOpsBars } from './healthBars';
import ModelOpsNav from './ModelOpsNav';
import { ObservationDialog, observationSummaryLine } from './ObservationDialog';
import { isObservingSource, sourceAdapterLabel } from './sourceStatus';
import { sourceTypeLabel } from './SourceTypeBadge';
import { Button } from '../../components/ui/button';
import { Checkbox } from '../../components/ui/checkbox';
import ConsolePage from '../../components/ui/console-page';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../../components/ui/dropdown-menu';
import { IconButton } from '../../components/ui/icon-button';
import { Input } from '../../components/ui/input';
import { resolveProviderBrand } from '../../components/ui/provider-brands';
import { ProviderLogo } from '../../components/ui/provider-icons';
import { deferUndoable, useUndoPending } from '../../components/ui/undo-toast';
import { API } from '../../helpers/api';
import { useChannelsQuery } from '../../hooks/channels/useChannelsQuery';
import { useChannelLanesQuery } from '../../hooks/models/useModelOpsDimensions';
import {
  useModelOpsMutations,
  useModelOpsSourcesQuery,
  type ModelOpsSource,
} from '../../hooks/models/useModelOpsQuery';
import { unwrap } from '../../lib/api-error';
import type { TFunction } from 'i18next';
import './model-ops.css';
import './ledger.css';
import './source-pages.css';
import '../../components/ui/row-actions.css';

const ns = 'page.console.modelOps';
const WINDOW_HOURS = 48;
const LIST = '/console/model-ops/sources';
/* Twelve slices fit the health column; the window arrives finer. */
const BAR_SLICES = 12;

type View = 'all' | 'changed' | 'live' | 'observing' | 'off';
type OptionalColumn = 'health' | 'models' | 'upstream';

const viewOf = (source: ModelOpsSource): Exclude<View, 'all' | 'changed'> =>
  source.status !== 1
    ? 'off'
    : isObservingSource(source)
      ? 'observing'
      : 'live';

const upstreamChange = (source: ModelOpsSource) => ({
  gone: source.upstream_sync?.gone ?? 0,
  new: source.upstream_sync?.new ?? 0,
});
const hasChange = (source: ModelOpsSource) => {
  const change = upstreamChange(source);
  return change.new + change.gone > 0 || Boolean(source.upstream_sync?.error);
};

/* A click that landed on a control inside the row belongs to that control. */
const fromControl = (event: React.MouseEvent | React.KeyboardEvent) =>
  Boolean(
    (event.target as HTMLElement | null)?.closest(
      'a, button, input, label, [role="menu"], [role="menuitem"]',
    ),
  );

function StatusCell({
  onOpenObservation,
  source,
  t,
}: {
  onOpenObservation: (source: ModelOpsSource) => void;
  source: ModelOpsSource;
  t: TFunction;
}) {
  const view = viewOf(source);
  const observation = source.observation;
  if (view === 'off') {
    return (
      <span className='src-state'>
        <span className='src-state__line'>
          <span className='src-badge is-off'>
            {t(`${ns}.sources.stateOff`, { defaultValue: '停用' })}
          </span>
        </span>
      </span>
    );
  }
  if (view === 'observing' && observation) {
    const day = observation.complete
      ? observation.days_required
      : observation.day;
    const progress = observation.complete
      ? 1
      : Math.min(1, observation.day / (observation.days_required || 1));
    const summary = observation.summary;
    const parts = summary ? (observationSummaryLine(summary, t) ?? []) : [];
    const problems = summary?.problem_count ?? 0;
    return (
      <span className='src-state'>
        <span className='src-state__line'>
          <span className='src-badge is-watch'>
            {t(`${ns}.sources.observing`, { defaultValue: '观察中' })}
          </span>
          <span className='src-hint src-mono'>
            {t(`${ns}.sources.observeDays`, {
              day,
              defaultValue: '{{day}}/{{total}} 天',
              total: observation.days_required,
            })}
          </span>
        </span>
        <button
          data-owns-chrome=''
          className='src-state__line src-state__watch'
          type='button'
          onClick={() => onOpenObservation(source)}
        >
          <span
            aria-label={t(`${ns}.sources.observeProgress`, {
              day,
              defaultValue: '观察进度 {{day}}/{{total}} 天',
              total: observation.days_required,
            })}
            className='src-track'
            role='img'
          >
            <b style={{ width: `${Math.round(progress * 100)}%` }} />
          </span>
          {problems > 0 ? (
            <span className='src-state__problems'>
              <AlertTriangle aria-hidden='true' size={12} />
              {t(`${ns}.sources.problems`, {
                count: problems,
                defaultValue: '{{count}} 个有问题',
              })}
            </span>
          ) : (
            <span className='src-hint'>
              {parts.map((part) => part.text).join(' · ') ||
                t(`${ns}.sources.goLiveCheck`, {
                  count: observation.models ?? 0,
                  defaultValue: '上线检查 · {{count}}',
                })}
            </span>
          )}
        </button>
      </span>
    );
  }
  return (
    <span className='src-state'>
      <span className='src-state__line'>
        <span className='src-badge is-live'>
          {t(`${ns}.sources.stateLive`, { defaultValue: '正式' })}
        </span>
        {observation ? (
          <button
            data-owns-chrome=''
            className='src-hint src-state__some'
            type='button'
            onClick={() => onOpenObservation(source)}
          >
            {t(`${ns}.sources.someObserving`, {
              count: observation.models ?? 0,
              defaultValue: '{{count}} 个观察中',
            })}
          </button>
        ) : null}
      </span>
    </span>
  );
}

/**
 * 模型运营 · 来源 — the supply side, one row per upstream account. A row
 * opens the source's own page; a new source is a page too.
 */
export default function SourcesPage() {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const channels = useChannelsQuery({ listEnabled: false });
  const { t } = channels;
  const modelFilter = searchParams.get('model')?.trim() || '';
  const sourcesQuery = useModelOpsSourcesQuery(modelFilter, WINDOW_HOURS);
  const ops = useModelOpsMutations(t);
  const lanes = useChannelLanesQuery().data ?? [];
  const defaultLane = lanes[0]?.slug;

  const [keyword, setKeyword] = useState('');
  const [view, setView] = useState<View>('all');
  const [hidden, setHidden] = useState<Set<OptionalColumn>>(new Set());
  const [observed, setObserved] = useState<ModelOpsSource | null>(null);

  const sources = useMemo(
    () => sourcesQuery.data?.items || [],
    [sourcesQuery.data?.items],
  );
  const windowHours = sourcesQuery.data?.rules?.window_hours ?? WINDOW_HOURS;

  const base = useMemo(() => {
    const needle = keyword.trim().toLowerCase();
    if (!needle) return sources;
    return sources.filter((source) => {
      const lane = lanes.find((item) => item.slug === source.lane)?.name;
      const haystack =
        `${source.name} ${source.endpoint} ${source.type_name} ${lane || ''} ${(source.model_names || []).join(' ')}`.toLowerCase();
      return haystack.includes(needle);
    });
  }, [keyword, lanes, sources]);
  const inView: Record<View, (source: ModelOpsSource) => boolean> = {
    all: () => true,
    changed: hasChange,
    live: (source) => viewOf(source) === 'live',
    observing: (source) => viewOf(source) === 'observing',
    off: (source) => viewOf(source) === 'off',
  };
  const rows = base.filter(inView[view]);

  const undoPending = useUndoPending();
  const isUndoPending = (source: ModelOpsSource) =>
    undoPending.has(`channel:${String(source.id)}`);
  /* 删除 runs at once and offers a 10-second undo instead of a dialog;
     the row reads 删除中 until the request actually goes out. */
  const deleteNow = (source: ModelOpsSource) => {
    const name = source.name;
    const path = `/api/channel/${source.id}/`;
    deferUndoable({
      keys: [`channel:${String(source.id)}`],
      message: t('undo.channel.deleted', {
        defaultValue: '已删除 {{name}}',
        name,
      }),
      restored: t('undo.channel.restored', {
        defaultValue: '已恢复 {{name}}',
        name,
      }),
      failed: t('undo.channel.failed', {
        defaultValue: '没删成，{{name}} 已放回',
        name,
      }),
      // The undo toast reports a failure itself, with 重试.
      commit: async () =>
        unwrap(await API.delete(path, { skipErrorHandler: true })),
      keepalive: { method: 'DELETE', path },
      onCommitted: channels.refresh,
    });
  };

  const open = (source: ModelOpsSource, tab?: string) =>
    navigate(`${LIST}/${source.id}${tab ? `?tab=${tab}` : ''}`);

  /* Old links: ?new=1 and ?source=<id> opened the dialog this page used to
     have. They land on the pages that replaced it. */
  const legacySource = searchParams.get('source');
  if (searchParams.get('new') === '1') {
    return <Navigate replace to={`${LIST}/new`} />;
  }
  if (legacySource) {
    return <Navigate replace to={`${LIST}/${legacySource}`} />;
  }

  const visible = (key: OptionalColumn) => !hidden.has(key);
  const template = [
    '36px',
    'minmax(0, 1.6fr)',
    '190px',
    visible('upstream') ? '120px' : null,
    visible('models') ? '70px' : null,
    visible('health') ? '200px' : null,
    '28px',
  ]
    .filter(Boolean)
    .join(' ');
  const columns: Array<[OptionalColumn, string]> = [
    ['upstream', t(`${ns}.sources.colUpstream`, { defaultValue: '上游变化' })],
    ['models', t(`${ns}.sources.colModels`, { defaultValue: '模型' })],
    [
      'health',
      t(`${ns}.sources.colHealth`, {
        defaultValue: '{{hours}} 小时健康',
        hours: windowHours,
      }),
    ],
  ];

  return (
    <ConsolePage
      appearance='review'
      className='model-ops-page--compact src-page src-page--list'
      title={t(`${ns}.title`, { defaultValue: '模型运营' })}
      tabs={<ModelOpsNav t={t} />}
      actions={
        <>
          <Button
            loading={ops.testAllSourcesMutation.isPending}
            size='sm'
            variant='secondary'
            onClick={() => ops.testAllSourcesMutation.mutate()}
          >
            <Zap size={14} />
            {t(`${ns}.sources.testAll`, { defaultValue: '测试全部' })}
          </Button>
          <Button
            size='sm'
            variant='primary'
            onClick={() => navigate(`${LIST}/new`)}
          >
            <Plus size={14} />
            {t(`${ns}.sources.create`, { defaultValue: '新建来源' })}
          </Button>
        </>
      }
    >
      <section className='src-panel src-list'>
        <div className='src-toolbar'>
          <span className='lazu-input-affix src-search'>
            <span className='lazu-input-affix__prefix'>
              <Search aria-hidden='true' size={14} />
            </span>
            <Input
              aria-label={t(`${ns}.sources.searchLabel`, {
                defaultValue: '搜索来源',
              })}
              placeholder={t(`${ns}.sources.searchShort`, {
                defaultValue: '搜来源或模型',
              })}
              value={keyword}
              onChange={(event) => setKeyword(event.target.value)}
            />
          </span>
          <div
            aria-label={t(`${ns}.sources.statusFilter`, {
              defaultValue: '状态',
            })}
            className='src-seg'
            role='group'
          >
            {(
              [
                ['all', t(`${ns}.sources.viewAll`, { defaultValue: '全部' })],
                [
                  'live',
                  t(`${ns}.sources.stateLive`, { defaultValue: '正式' }),
                ],
                [
                  'observing',
                  t(`${ns}.sources.observing`, { defaultValue: '观察中' }),
                ],
                [
                  'changed',
                  t(`${ns}.sources.viewChanged`, {
                    defaultValue: '上游有变化',
                  }),
                ],
                ['off', t(`${ns}.sources.stateOff`, { defaultValue: '停用' })],
              ] as Array<[View, string]>
            ).map(([key, label]) => (
              <button
                data-owns-chrome=''
                aria-pressed={view === key}
                className={`src-seg__item${view === key ? ' is-on' : ''}`}
                key={key}
                type='button'
                onClick={() => setView(key)}
              >
                {label}
                <span className='src-num'>
                  {base.filter(inView[key]).length}
                </span>
              </button>
            ))}
          </div>
          <span className='src-toolbar__end'>
            <DropdownMenu>
              <DropdownMenuTrigger>
                <Button size='sm' variant='secondary'>
                  <Columns3 size={14} />
                  {t(`${ns}.sources.columns`, { defaultValue: '列' })}
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align='end' className='src-colmenu'>
                {columns.map(([key, label]) => (
                  <Checkbox
                    checked={visible(key)}
                    key={key}
                    labelClassName='src-colmenu__item'
                    onCheckedChange={(checked: boolean) =>
                      setHidden((previous) => {
                        const next = new Set(previous);
                        if (checked) next.delete(key);
                        else next.add(key);
                        return next;
                      })
                    }
                  >
                    {label}
                  </Checkbox>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          </span>
        </div>

        <div
          aria-label={t(`${ns}.sources.colName`, { defaultValue: '来源' })}
          className='src-table'
          role='table'
        >
          <div
            className='src-srow src-srow--head'
            role='row'
            style={{ gridTemplateColumns: template }}
          >
            <span role='columnheader' />
            <span className='src-th' role='columnheader'>
              {t(`${ns}.sources.colName`, { defaultValue: '来源' })}
            </span>
            <span className='src-th' role='columnheader'>
              {t(`${ns}.sources.colState`, { defaultValue: '状态' })}
            </span>
            {columns
              .filter(([key]) => visible(key))
              .map(([key, label]) => (
                <span
                  className={`src-th${key === 'upstream' ? '' : ' is-num'}`}
                  key={key}
                  role='columnheader'
                >
                  {label}
                </span>
              ))}
            <span role='columnheader' />
          </div>

          {sourcesQuery.isLoading ? (
            <div className='src-empty'>
              <span className='mo-state'>
                <span aria-hidden='true' className='mo-state__spinner' />
                {t(`${ns}.sources.loading`, { defaultValue: '正在读取来源' })}
              </span>
            </div>
          ) : sources.length === 0 ? (
            <div className='src-empty src-empty--tall'>
              <div className='lazu-state-title'>
                {t(`${ns}.sources.emptyTitle`, { defaultValue: '还没有来源' })}
              </div>
              <div className='lazu-state-description'>
                {t(`${ns}.sources.emptyDescription`, {
                  defaultValue: '接一个来源，模型和供货才会进来。',
                })}
              </div>
              <Button
                size='sm'
                variant='primary'
                onClick={() => navigate(`${LIST}/new`)}
              >
                {t(`${ns}.sources.create`, { defaultValue: '新建来源' })}
              </Button>
            </div>
          ) : rows.length === 0 ? (
            <div className='src-empty'>
              {t(`${ns}.sources.noMatch`, {
                defaultValue: '没有符合筛选的来源',
              })}
            </div>
          ) : (
            rows.map((source) => {
              const deleting = isUndoPending(source);
              const tone = healthTone(source.health, source.status);
              const all = source.health.buckets || [];
              const measured = all.some(
                (bucket) => bucket.success_rate !== null,
              );
              const bars = measured
                ? foldBuckets(all, Math.ceil(all.length / BAR_SLICES))
                : [];
              const observing = viewOf(source) === 'observing';
              const lane = lanes.find((item) => item.slug === source.lane);
              const showLane =
                lanes.length > 1 && source.lane && source.lane !== defaultLane;
              const change = upstreamChange(source);
              const syncError = source.upstream_sync?.error;
              return (
                <div
                  aria-label={source.name}
                  className={`src-srow${deleting ? ' is-undo-pending' : ''}`}
                  key={source.id}
                  role='row'
                  style={{ gridTemplateColumns: template }}
                  tabIndex={deleting ? -1 : 0}
                  onClick={(event) => {
                    if (deleting || fromControl(event)) return;
                    open(source);
                  }}
                  onKeyDown={(event) => {
                    if (deleting || fromControl(event)) return;
                    if (event.key === 'Enter') {
                      event.preventDefault();
                      open(source);
                    }
                  }}
                >
                  <span className='src-logo' role='cell'>
                    <ProviderLogo
                      brand={resolveProviderBrand({
                        name: source.type_name || source.name,
                      })}
                      decorative
                      size={16}
                    />
                  </span>
                  <span className='src-sname' role='cell'>
                    <span className='src-sname__name'>{source.name}</span>
                    <span className='src-sname__meta'>
                      <span className='src-tag'>
                        {sourceTypeLabel(source.source_type, t)}
                      </span>
                      {showLane ? (
                        <span className='src-tag'>
                          <span
                            aria-hidden='true'
                            className={`mo-dot mo-dot--lane-${lane?.color || 'info'}`}
                          />
                          {lane?.name || source.lane}
                        </span>
                      ) : null}
                      <span className='src-hint'>
                        {sourceAdapterLabel(source, t)}
                      </span>
                    </span>
                  </span>
                  <span role='cell'>
                    {deleting ? (
                      <span className='src-badge is-off'>
                        {t('undo.deleting', { defaultValue: '删除中' })}
                      </span>
                    ) : (
                      <StatusCell
                        source={source}
                        t={t}
                        onOpenObservation={setObserved}
                      />
                    )}
                  </span>
                  {visible('upstream') ? (
                    <span role='cell'>
                      {change.new + change.gone > 0 ? (
                        <button
                          data-owns-chrome=''
                          className='src-diff'
                          type='button'
                          onClick={() => navigate(`${LIST}/${source.id}`)}
                        >
                          {change.new ? (
                            <span className='src-diff__new'>+{change.new}</span>
                          ) : null}
                          {change.gone ? (
                            <span className='src-diff__gone'>
                              −{change.gone}
                            </span>
                          ) : null}
                          <span className='src-hint'>
                            {t(`${ns}.sources.toReview`, {
                              defaultValue: '待核对',
                            })}
                          </span>
                        </button>
                      ) : syncError ? (
                        <button
                          data-owns-chrome=''
                          className='src-diff src-diff--bad'
                          title={syncError}
                          type='button'
                          onClick={() => open(source, 'sync')}
                        >
                          {t(`${ns}.sources.checkFailed`, {
                            defaultValue: '检查失败',
                          })}
                        </button>
                      ) : (
                        <span className='src-hint'>—</span>
                      )}
                    </span>
                  ) : null}
                  {visible('models') ? (
                    <span className='src-mono is-num' role='cell'>
                      {source.model_count}
                    </span>
                  ) : null}
                  {visible('health') ? (
                    <span className='src-health' role='cell'>
                      {bars.length ? (
                        <span className='src-health__line'>
                          <ModelOpsBars
                            buckets={bars}
                            height={16}
                            label={t(`${ns}.sources.probeTitle`, {
                              defaultValue:
                                '{{hours}} 小时内按等分时段的成功率',
                              hours: windowHours,
                            })}
                            probed={
                              source.health.source === 'probe'
                                ? observing
                                  ? 'observing'
                                  : 'idle'
                                : undefined
                            }
                          />
                          <span
                            className={`src-mono src-health__rate is-${tone}`}
                          >
                            {source.health.request_count > 0
                              ? formatRate(source.health.success_rate)
                              : '—'}
                          </span>
                        </span>
                      ) : null}
                      <span className='src-hint'>
                        {!bars.length
                          ? t(`${ns}.sources.notMeasured`, {
                              defaultValue: '尚未观测',
                            })
                          : observing
                            ? t(`${ns}.sources.probeShort`, {
                                defaultValue: '抽测 {{ok}}/{{total}}',
                                ok: (
                                  source.health.request_count -
                                  source.health.error_count
                                ).toLocaleString(),
                                total:
                                  source.health.request_count.toLocaleString(),
                              })
                            : source.health.request_count > 0
                              ? t(`${ns}.sources.healthFigures`, {
                                  defaultValue:
                                    '均值 {{latency}} · {{requests}} 次',
                                  latency: formatLatency(
                                    source.health.avg_latency_ms,
                                  ),
                                  requests:
                                    source.health.request_count.toLocaleString(),
                                })
                              : t(`${ns}.sources.idleWindow`, {
                                  defaultValue: '窗口内无流量',
                                })}
                      </span>
                    </span>
                  ) : null}
                  <span className='src-more' role='cell'>
                    {deleting ? null : (
                      <DropdownMenu>
                        <DropdownMenuTrigger>
                          <IconButton
                            aria-label={t(`${ns}.sources.more`, {
                              defaultValue: '更多操作 {{name}}',
                              name: source.name,
                            })}
                            size='sm'
                            variant='ghost'
                          >
                            <MoreHorizontal size={16} />
                          </IconButton>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent
                          align='end'
                          className='min-w-[160px]'
                        >
                          <DropdownMenuItem onSelect={() => open(source)}>
                            <span className='lazu-dropdown-item__icon'>
                              <Pencil size={14} />
                            </span>
                            {t('page.console.tables.edit', {
                              defaultValue: '编辑',
                            })}
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onSelect={() =>
                              ops.testSourceMutation.mutate(source.id)
                            }
                          >
                            <span className='lazu-dropdown-item__icon'>
                              <Zap size={14} />
                            </span>
                            {t(`${ns}.sources.test`, { defaultValue: '测试' })}
                          </DropdownMenuItem>
                          {source.observation ? (
                            <DropdownMenuItem
                              onSelect={() => setObserved(source)}
                            >
                              <span className='lazu-dropdown-item__icon'>
                                <ShieldCheck size={14} />
                              </span>
                              {t(`${ns}.sources.goLiveCheck`, {
                                count: source.observation.models ?? 0,
                                defaultValue: '上线检查 · {{count}}',
                              })}
                            </DropdownMenuItem>
                          ) : null}
                          <DropdownMenuItem
                            onSelect={() =>
                              ops.sourceStatusMutation.mutate({
                                enabled: source.status !== 1,
                                source,
                              })
                            }
                          >
                            <span className='lazu-dropdown-item__icon'>
                              <Power size={14} />
                            </span>
                            {source.status === 1
                              ? t(`${ns}.sources.disable`, {
                                  defaultValue: '停用',
                                })
                              : t(`${ns}.sources.enable`, {
                                  defaultValue: '启用',
                                })}
                          </DropdownMenuItem>
                          <div
                            className='lazu-dropdown-separator'
                            role='separator'
                          />
                          <DropdownMenuItem
                            className='lazu-dropdown-item--danger'
                            onSelect={() => deleteNow(source)}
                          >
                            <span className='lazu-dropdown-item__icon'>
                              <Trash2 size={14} />
                            </span>
                            {t('page.console.tables.delete', {
                              defaultValue: '删除',
                            })}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    )}
                  </span>
                </div>
              );
            })
          )}
        </div>
        <div className='src-foot'>
          <span>
            {t(`${ns}.sources.total`, {
              count: rows.length,
              defaultValue: '共 {{count}} 个来源',
            })}
          </span>
        </div>
      </section>

      <ObservationDialog
        busy={
          ops.goLiveSourceMutation.isPending ||
          ops.removeWatchedModelsMutation.isPending
        }
        source={observed}
        t={t}
        onGoLive={async (models) => {
          if (!observed) return;
          await ops.goLiveSourceMutation.mutateAsync({
            models,
            source: observed,
          });
          // Nothing left to watch: the dialog has nothing to show.
          if (models.length >= (observed.observation?.routes ?? 0)) {
            setObserved(null);
          }
        }}
        onOpenChange={(open) => {
          if (
            !open &&
            !ops.goLiveSourceMutation.isPending &&
            !ops.removeWatchedModelsMutation.isPending
          )
            setObserved(null);
        }}
        onRemove={async (models) => {
          if (!observed) return;
          await ops.removeWatchedModelsMutation.mutateAsync({
            models,
            source: observed,
          });
          if (models.length >= (observed.observation?.routes ?? 0)) {
            setObserved(null);
          }
        }}
      />
    </ConsolePage>
  );
}

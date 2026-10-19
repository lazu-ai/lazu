import { useQueryClient } from '@tanstack/react-query';
import { Boxes } from 'lucide-react';
import { useCallback, useDeferredValue, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { StatusBanner, StatusBoard } from './status-board';
import {
  Badge,
  Button,
  EmptyState,
  ErrorState,
  Input,
  LoadingState,
  Modal,
  Select,
} from '../../components/ui';
import ConsolePage from '../../components/ui/console-page';
import { API } from '../../helpers/api';
import {
  getRelativeTime,
  isRoot as isPlatformAdmin,
  showError,
  showSuccess,
} from '../../helpers/utils';
import {
  channelHealthQueryKey,
  useChannelHealthQuery,
  useChannelHealthRetreatMutation,
  useChannelHealthRoleMutation,
  type ChannelHealthBucket,
  type ChannelHealthChannel,
  type ChannelHealthKind,
  type ChannelHealthKindSection,
  type ChannelHealthRole,
  type ChannelHealthProvider,
  type ChannelHealthProviderLane,
  type ChannelHealthRow,
  type ChannelHealthStatus,
} from '../../hooks/health/useChannelHealthQuery';
import { unwrap } from '../../lib/api-error';
import './channel-health.css';

const ns = 'page.console.channelHealth';
/** The console's one window: a week, drawn status-page style. */
const HEALTH_WINDOW = '7d' as const;

type LaneFilter = 'all' | 'stable' | 'eco';
type KindFilter = 'all' | ChannelHealthKind;

/** Sections in the order the server sends them; this is the fallback for a
 *  payload written before sections existed. */
const KIND_ORDER: ChannelHealthKind[] = [
  'llm',
  'embedding',
  'rerank',
  'decision',
  'image',
  'audio',
  'video',
  'search',
];

const rowKind = (row: ChannelHealthRow): ChannelHealthKind => row.kind ?? 'llm';

/** How many model chips a provider card shows before it counts the rest. */
const MODEL_CHIP_LIMIT = 8;
/** Two readings of one window: by upstream (admin only) or by model. */
type HealthView = 'provider' | 'model';

const STATUS_VARIANT: Record<
  ChannelHealthStatus,
  'success' | 'warning' | 'danger' | 'neutral'
> = {
  degraded: 'warning',
  operational: 'success',
  outage: 'danger',
  unknown: 'neutral',
};

const formatPercent = (value?: number) =>
  value == null
    ? '—'
    : `${(value < 1 ? Math.min(value * 100, 99.9) : 100).toFixed(1)}%`;

const formatMs = (value?: number) => {
  if (value == null || value <= 0) return '—';
  return value >= 1000
    ? `${(value / 1000).toFixed(2)}s`
    : `${Math.round(value)}ms`;
};

const formatTps = (value?: number) =>
  value == null || value <= 0 ? '—' : value.toFixed(0);

const formatShare = (value?: number) =>
  value == null ? '—' : `${(value * 100).toFixed(0)}%`;

/** A lane with no evidence at all is not "100%" — it has nothing to report. */
const rateToneClass = (row: ChannelHealthRow) => {
  if (row.status === 'outage') return 'chealth-row__pct chealth-row__pct--bad';
  if (row.status === 'degraded')
    return 'chealth-row__pct chealth-row__pct--warn';
  return 'chealth-row__pct';
};

const statusToneClass = (status: ChannelHealthStatus) => {
  if (status === 'outage') return 'chealth-tone--bad';
  if (status === 'degraded') return 'chealth-tone--warn';
  if (status === 'unknown') return 'chealth-tone--none';
  return '';
};

export function RowHealthStatus({
  row,
  label,
}: {
  row: ChannelHealthRow;
  label: string;
}) {
  return (
    <span className={rateToneClass(row)}>
      <i
        aria-hidden='true'
        className={`chealth-status-mark chealth-status-mark--${row.status}`}
      />
      <span className='chealth-row__status-label'>{label}</span>
      <span className='chealth-row__rate'>
        {formatPercent(row.success_rate)}
      </span>
    </span>
  );
}

export function InactiveSourceBadge({ label }: { label: string }) {
  return <Badge variant='neutral'>{label}</Badge>;
}

/** A provider card is a third of the page wide; the week's 84 slices would
 *  be hairlines there, so a card draws them three at a time. */
const PROVIDER_CARD_SLICES = 28;

const TONE_SEVERITY: Record<ChannelHealthBucket['tone'], number> = {
  bad: 4,
  warn: 3,
  ok: 2,
  blocked: 1,
  none: 0,
};

/** Merges neighbouring slices into `slices` wider ones. A merged slice shows
 *  its worst verdict, so a red hour never disappears into a green day. */
export const compactBuckets = (
  buckets: ChannelHealthBucket[],
  slices: number,
): ChannelHealthBucket[] =>
  Array.from({ length: slices }, (_unused, index) => {
    const group = buckets.slice(
      Math.floor((index * buckets.length) / slices),
      Math.floor(((index + 1) * buckets.length) / slices),
    );
    // Keep the timestamp, rate, source and counts from the same worst
    // slice. Summing healthy neighbours would contradict its verdict.
    const worst = group.reduce<ChannelHealthBucket | undefined>(
      (current, bucket) => {
        if (
          !current ||
          TONE_SEVERITY[bucket.tone] > TONE_SEVERITY[current.tone] ||
          (bucket.tone === current.tone &&
            (bucket.success_rate ?? 1) < (current.success_rate ?? 1))
        )
          return bucket;
        return current;
      },
      undefined,
    );
    const merged: ChannelHealthBucket = worst
      ? { ...worst, index }
      : {
          index,
          start_at: '',
          source: 'none',
          request_count: 0,
          error_count: 0,
          tone: 'none',
        };
    return merged;
  });

/* One mark per verdict, in one weight. A cell used to be drawn four ways —
   solid for customer traffic, hollow for a probe, faded for a verdict carried
   from an earlier slice — and a reader saw a wall of uneven greens that said
   less about the upstream than about our own schedule. A cell now holds only
   what was measured inside it, and where the sample came from lives in the
   tooltip. The one exception is a route we cannot check at all: that is
   hatched, and only an operator ever receives it. */
const cellClassName = (bucket: ChannelHealthBucket) => {
  const classes = ['chealth-cell', `chealth-cell--${bucket.tone}`];
  if (bucket.source === 'blocked') classes.push('chealth-cell--blocked');
  return classes.join(' ');
};

export function HeatCells({
  buckets,
  cellLabel,
  onInspect,
  size,
}: {
  buckets: ChannelHealthBucket[];
  cellLabel: (bucket: ChannelHealthBucket) => string;
  onInspect?: () => void;
  size?: 'lane' | 'provider';
}) {
  return (
    <span
      className={`chealth-cells${size === 'lane' ? ' chealth-cells--lane' : ''}`}
    >
      {buckets.map((bucket) =>
        onInspect ? (
          <button
            aria-label={cellLabel(bucket)}
            className={cellClassName(bucket)}
            data-label={cellLabel(bucket)}
            data-owns-chrome='true'
            key={bucket.index}
            onClick={onInspect}
            onFocus={onInspect}
            title={cellLabel(bucket)}
            type='button'
          />
        ) : (
          <span
            aria-hidden='true'
            className={cellClassName(bucket)}
            data-label={cellLabel(bucket)}
            key={bucket.index}
            title={cellLabel(bucket)}
          />
        ),
      )}
    </span>
  );
}

/** The status shape doubles as the legend key, so it is drawn once here and
 *  reused by every surface that reports a verdict. */
function StatusMark({ status }: { status: ChannelHealthStatus }) {
  return (
    <i
      aria-hidden='true'
      className={`chealth-status-mark chealth-status-mark--${status}`}
    />
  );
}

export function ChannelHealthPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [laneFilter, setLaneFilter] = useState<LaneFilter>('all');
  const [searchParams, setSearchParams] = useSearchParams();
  const [search, setSearch] = useState('');
  const kindFilter = (searchParams.get('kind') || 'all') as KindFilter;
  const setKindFilter = useCallback(
    (next: KindFilter) => {
      const params = new URLSearchParams(searchParams);
      if (next === 'all') params.delete('kind');
      else params.set('kind', next);
      setSearchParams(params, { replace: true });
    },
    [searchParams, setSearchParams],
  );
  const [selectedProviderId, setSelectedProviderId] = useState<number | null>(
    null,
  );
  const [pauseTarget, setPauseTarget] = useState<ChannelHealthProvider | null>(
    null,
  );
  const [pausing, setPausing] = useState(false);
  const [floorTarget, setFloorTarget] = useState<ChannelHealthProvider | null>(
    null,
  );
  const [flooring, setFlooring] = useState(false);
  const [testingId, setTestingId] = useState<number | null>(null);

  /* One window, the week. The status word is the breaker's verdict now; the
     bars and the availability are the week behind it, the span a status page
     reader expects. A day showed too little and asked the reader to choose. */
  const health = useChannelHealthQuery(HEALTH_WINDOW);
  const roleMutation = useChannelHealthRoleMutation();
  const retreatMutation = useChannelHealthRetreatMutation();
  const payload = health.data;
  const isAdmin = Boolean(payload?.is_admin);
  // The page chrome must not wait for the payload, or an operator's title
  // would flip from the customer wording once the request lands. The local
  // role answers synchronously; the payload still governs the data views.
  const operator = isPlatformAdmin();
  const providers = useMemo(() => payload?.providers ?? [], [payload]);

  // The by-upstream view is an admin-only operations lens. The locked health
  // surface remains the model-by-lane heat wall unless an operator explicitly
  // asks for the alternate projection in the URL.
  const requestedView = searchParams.get('view');
  const view: HealthView =
    isAdmin && requestedView === 'provider' ? 'provider' : 'model';
  const setView = useCallback(
    (next: HealthView) => {
      const params = new URLSearchParams(searchParams);
      if (next === 'provider') params.set('view', next);
      else params.delete('view');
      setSearchParams(params, { replace: true });
    },
    [searchParams, setSearchParams],
  );

  const laneLabel = useCallback(
    (lane: string) =>
      lane === 'eco'
        ? t(`${ns}.lane.eco`, { defaultValue: 'Agent' })
        : t(`${ns}.lane.stable`, { defaultValue: '稳定' }),
    [t],
  );

  const statusLabel = (status: ChannelHealthStatus) =>
    t(`observability.status.${status}`, {
      defaultValue: status,
    });

  // Source-type labels already have an owner in 模型运营; the ledger reads the
  // same five values, so it reuses them instead of keeping a second copy that
  // could drift.
  const sourceTypeLabel = (sourceType: string) =>
    t(`page.console.modelOps.sourceType.${sourceType}`, {
      defaultValue: sourceType,
    });

  const sourceLabel = useCallback(
    (source: ChannelHealthRow['source']) => {
      if (source === 'traffic')
        return t(`${ns}.source.traffic`, { defaultValue: '实测流量' });
      if (source === 'probe')
        return t(`${ns}.source.probe`, { defaultValue: '探测' });
      if (source === 'blocked')
        return t(`${ns}.source.blocked`, { defaultValue: '无法探测' });
      return t(`${ns}.source.none`, { defaultValue: '无数据' });
    },
    [t],
  );

  const roleLabel = (role?: ChannelHealthRole) => {
    if (role === 'backup')
      return t(`${ns}.role.backup`, { defaultValue: '备用' });
    if (role === 'inventory')
      return t(`${ns}.role.inventory`, { defaultValue: '库存' });
    return t(`${ns}.role.primary`, { defaultValue: '主用' });
  };

  const deferredSearch = useDeferredValue(search);
  const query = deferredSearch.trim().toLowerCase();

  const rows = useMemo(
    () =>
      (payload?.rows || []).filter((row) => {
        if (kindFilter !== 'all' && rowKind(row) !== kindFilter) return false;
        // Only text models are sold on two lanes; the lane filter reads as a
        // question about them and leaves the other sections alone.
        if (
          laneFilter !== 'all' &&
          rowKind(row) === 'llm' &&
          row.lane !== laneFilter
        )
          return false;
        return query ? row.model_name.toLowerCase().includes(query) : true;
      }),
    [kindFilter, laneFilter, payload?.rows, query],
  );

  /* The sections the payload holds, in the server's order, each with the
     slice width its wall is drawn in. The kind chips offer exactly these, so
     a filter never leads to a section that cannot exist. */
  const sections = useMemo<ChannelHealthKindSection[]>(() => {
    if (payload?.kinds?.length) return payload.kinds;
    const present = new Set((payload?.rows || []).map(rowKind));
    return KIND_ORDER.filter((kind) => present.has(kind)).map((kind) => ({
      kind,
      bucket_count: payload?.bucket_count || 24,
      bucket_seconds: payload?.bucket_seconds || 3600,
      row_count: (payload?.rows || []).filter((row) => rowKind(row) === kind)
        .length,
    }));
  }, [payload]);

  const visibleProviders = useMemo(() => {
    return providers.filter((provider) => {
      if (laneFilter !== 'all') {
        const servesLane = provider.lanes.some(
          (lane) => lane.lane === laneFilter,
        );
        if (!servesLane) return false;
      }
      if (!query) return true;
      // An upstream matches on its own name or on any model it serves, so the
      // same search box answers both "where is this source" and "who serves gpt-5".
      return (
        provider.channel_name.toLowerCase().includes(query) ||
        provider.lanes.some((lane) =>
          lane.model_name.toLowerCase().includes(query),
        )
      );
    });
  }, [laneFilter, providers, query]);

  const selectedProvider = useMemo(() => {
    if (!visibleProviders.length) return null;
    return (
      visibleProviders.find(
        (provider) => provider.channel_id === selectedProviderId,
      ) ?? visibleProviders[0]
    );
  }, [selectedProviderId, visibleProviders]);

  /* What the page is showing, said once at the top instead of on every
     row: the week, how wide a bar is, and how fresh the aggregate is. */
  const boardMeta = payload
    ? [
        t(`${ns}.board.window`, {
          defaultValue: '过去 {{days}} 天',
          days: Math.max(1, Math.round(payload.window_seconds / 86400)),
        }),
        t(`${ns}.board.slice`, {
          defaultValue: '每格 {{hours}} 小时',
          hours: Math.round((payload.bucket_seconds / 3600) * 10) / 10,
        }),
        t(`${ns}.evidence.updated`, {
          defaultValue: '{{when}}更新',
          when: getRelativeTime(payload.updated_at),
        }),
      ].join(' · ')
    : null;

  const kindLabel = useCallback(
    (kind: ChannelHealthKind) =>
      t(`${ns}.kind.${kind}`, {
        defaultValue: {
          llm: '大模型',
          embedding: '向量模型',
          rerank: '重排序',
          decision: '判别模型',
          image: '图片生成',
          audio: '语音',
          video: '视频生成',
          search: '搜索工具',
        }[kind],
      }),
    [t],
  );

  const cellLabel = (bucket: ChannelHealthBucket) => {
    const when = bucket.start_at
      ? new Date(bucket.start_at).toLocaleString()
      : '';
    if (bucket.source === 'none') {
      return `${when} · ${t(`${ns}.cell.idle`, { defaultValue: '无任何证据' })}`;
    }
    if (bucket.source === 'blocked') {
      return `${when} · ${t(`${ns}.cell.blocked`, {
        defaultValue: '无法探测，缺少配置',
      })}`;
    }
    if (!isAdmin) return `${when} · ${formatPercent(bucket.success_rate)}`;
    return `${when} · ${formatPercent(bucket.success_rate)} · ${sourceLabel(bucket.source)} · ${bucket.request_count}`;
  };

  const summary = payload?.summary;

  const emptyBuckets = compactBuckets([], PROVIDER_CARD_SLICES);

  /* Two levers, and they do different things. Weight only re-proportions:
     the picker adds a base weight to every live route, so even weight 0 keeps
     a share of the traffic. Pausing the upstream is the only action that
     actually stops it. The copy below says exactly that, because an operator
     reaching for one during an incident has to know which one they got. */
  const liveLanes = (provider: ChannelHealthProvider) =>
    provider.lanes.filter((lane) => !lane.disabled);

  const refreshHealth = () =>
    queryClient.invalidateQueries({
      queryKey: channelHealthQueryKey(HEALTH_WINDOW),
    });

  /* Roles and retreats are the two things an operator changes from this page.
     Everything else here reports; these two act, so they live next to the
     evidence that prompts them rather than in a separate settings screen. */
  /* Always the slug the server sent, never one derived from the display key:
     `lane` shows two lanes to customers by collapsing every other one into
     稳定, so reversing it would write to 直连 for a source that only serves an
     operator-created lane. */
  const routeLane = (row: ChannelHealthRow) =>
    row.lane_slug || (row.lane === 'eco' ? 'cheap' : 'direct');

  /* Abilities are stored under the routed name, which for an aliased variant
     is not the catalog name the row is shown under. */
  const routeModel = (row: ChannelHealthRow, source: ChannelHealthChannel) =>
    source.ability_model || row.model_name;

  const changeRole = async (
    row: ChannelHealthRow,
    source: ChannelHealthChannel,
    role: ChannelHealthRole,
  ) => {
    if ((source.role || 'primary') === role) return;
    try {
      await roleMutation.mutateAsync({
        channel_id: source.channel_id,
        lane: routeLane(row),
        model: routeModel(row, source),
        role,
      });
      showSuccess(
        t(`${ns}.panel.roleDone`, {
          defaultValue: '{{name}} 在这条线路上改为{{role}}',
          name: source.channel_name,
          role: roleLabel(role),
        }),
      );
      await refreshHealth();
    } catch {
      /* The request interceptor has reported the failure. */
    }
  };

  const clearRetreat = async (
    row: ChannelHealthRow,
    source: ChannelHealthChannel,
  ) => {
    try {
      await retreatMutation.mutateAsync({
        channel_id: source.channel_id,
        lane: routeLane(row),
        model: routeModel(row, source),
      });
      showSuccess(
        t(`${ns}.panel.restoreDone`, {
          defaultValue: '{{name}} 已重新进入轮转',
          name: source.channel_name,
        }),
      );
      await refreshHealth();
    } catch {
      /* The request interceptor has reported the failure. */
    }
  };

  const runFloor = async () => {
    if (!floorTarget) return;
    const routes = liveLanes(floorTarget).map((lane) => ({
      lane: lane.lane === 'eco' ? 'cheap' : 'direct',
      model: lane.model_name,
    }));
    if (!routes.length) {
      setFloorTarget(null);
      return;
    }
    setFlooring(true);
    try {
      unwrap<unknown>(
        await API.put('/api/model_ops/pool_weight/batch', {
          channel_id: String(floorTarget.channel_id),
          weight: 0,
          routes,
        }),
      );
      showSuccess(
        t(`${ns}.floor.done`, {
          count: routes.length,
          defaultValue: '已把 {{count}} 条通道上的权重降到 0',
        }),
      );
      setFloorTarget(null);
      await refreshHealth();
    } catch (error) {
      showError(error);
    } finally {
      setFlooring(false);
    }
  };

  const runPause = async () => {
    if (!pauseTarget) return;
    setPausing(true);
    try {
      unwrap<unknown>(
        await API.put(`/api/channel/${pauseTarget.channel_id}/status`, {
          status: 2,
          reason: 'paused from the channel health ledger',
        }),
      );
      showSuccess(
        t(`${ns}.pause.done`, {
          defaultValue: '已停用 {{name}}',
          name: pauseTarget.channel_name,
        }),
      );
      setPauseTarget(null);
      await refreshHealth();
    } catch (error) {
      showError(error);
    } finally {
      setPausing(false);
    }
  };

  const runTest = async (provider: ChannelHealthProvider) => {
    setTestingId(provider.channel_id);
    try {
      // Re-test the route responsible for the provider verdict before falling
      // back to an arbitrary live route. This also avoids rejecting a useful
      // idle probe merely because an unrelated alphabetical first route is hot.
      const lane =
        provider.lanes.find(
          (item) =>
            !item.disabled &&
            (item.status === 'outage' || item.status === 'degraded'),
        ) ??
        provider.lanes.find(
          (item) => !item.disabled && item.source === 'probe',
        ) ??
        liveLanes(provider)[0];
      if (!lane) {
        showError(t(`${ns}.provider.paused`, { defaultValue: '来源已停用' }));
        return;
      }
      try {
        // A failed probe is answered as channel_test_failed and reported by
        // the request interceptor; it is recorded either way.
        unwrap<unknown>(
          await API.post(`/api/channel/${provider.channel_id}/probe`, {
            model_name: lane.model_name,
            lane: lane.lane === 'eco' ? 'cheap' : 'direct',
          }),
        );
        showSuccess(
          t(`${ns}.test.done`, {
            defaultValue: '{{name}} 测试通过',
            name: provider.channel_name,
          }),
        );
      } finally {
        await refreshHealth();
      }
    } catch (error) {
      showError(error);
    } finally {
      setTestingId(null);
    }
  };

  return (
    <ConsolePage
      appearance='review'
      className='health-page--compact'
      /* One page, one wording, whoever is reading. An operator used to get a
         different title, a different description and a different word for the
         same thing; the only thing they actually need that a customer does not
         is the by-upstream lens, which renames itself below. */
      title={
        view === 'provider'
          ? t(`${ns}.title`, { defaultValue: '通道健康' })
          : t(`${ns}.customerTitle`, { defaultValue: '模型状态' })
      }
      description={
        view === 'provider'
          ? t(`${ns}.description.provider`, {
              defaultValue: '上游供应商谁在承载、谁在拖后腿。',
            })
          : t(`${ns}.description.model`, {
              defaultValue: '过去 7 天每个模型能不能用、快不快。',
            })
      }
      actions={
        boardMeta ? (
          <span className='chealth-board-meta'>{boardMeta}</span>
        ) : null
      }
    >
      {/* The lane tally ("4 条线路中断，另有 21 条降级") is an operator's alarm;
          a customer reads each model's own status in the board below. */}
      {isAdmin &&
      view === 'model' &&
      payload &&
      payload.rows.length > 0 &&
      !health.isError ? (
        <StatusBanner laneLabel={laneLabel} rows={payload.rows} />
      ) : null}

      <div className='chealth-filters'>
        {isAdmin ? (
          <div
            aria-label={t(`${ns}.view.label`, { defaultValue: '视角' })}
            className='chealth-viewseg'
            role='tablist'
          >
            <button
              aria-selected={view === 'provider'}
              className={`chealth-viewseg__item${view === 'provider' ? ' is-active' : ''}`}
              // index.css force-resets every plain button to a transparent
              // background; this attribute is that rule's documented opt-out,
              // and without it the selected tab is white text on white.
              data-owns-chrome='true'
              onClick={() => setView('provider')}
              role='tab'
              type='button'
            >
              {t(`${ns}.view.provider`, { defaultValue: '按供应商' })}
            </button>
            <button
              aria-selected={view === 'model'}
              className={`chealth-viewseg__item${view === 'model' ? ' is-active' : ''}`}
              data-owns-chrome='true'
              onClick={() => setView('model')}
              role='tab'
              type='button'
            >
              {t(`${ns}.view.model`, { defaultValue: '按模型' })}
            </button>
          </div>
        ) : null}

        {view === 'model' && sections.length > 1 ? (
          <div
            aria-label={t(`${ns}.kind.label`, { defaultValue: '种类' })}
            className='chealth-viewseg chealth-kindseg'
            role='tablist'
          >
            {(
              [
                'all',
                ...sections.map((section) => section.kind),
              ] as KindFilter[]
            ).map((kind) => (
              <button
                aria-selected={kindFilter === kind}
                className={`chealth-viewseg__item${kindFilter === kind ? ' is-active' : ''}`}
                data-owns-chrome='true'
                key={kind}
                onClick={() => setKindFilter(kind)}
                role='tab'
                type='button'
              >
                {kind === 'all'
                  ? t(`${ns}.kind.all`, { defaultValue: '全部' })
                  : kindLabel(kind)}
              </button>
            ))}
          </div>
        ) : null}

        <label className='chealth-filters__search'>
          <span className='sr-only'>
            {t(`${ns}.search`, { defaultValue: '搜索模型' })}
          </span>
          <Input
            onChange={(event) => setSearch(event.target.value)}
            placeholder={
              view === 'provider'
                ? t(`${ns}.searchProvider`, {
                    defaultValue: '搜索模型或供应商',
                  })
                : t(`${ns}.search`, { defaultValue: '搜索模型' })
            }
            value={search}
          />
        </label>
        <Select
          aria-label={t(`${ns}.filter.lane`, { defaultValue: '线路' })}
          value={laneFilter}
          onValueChange={(value) => setLaneFilter(value as LaneFilter)}
          options={[
            {
              value: 'all',
              label: t(`${ns}.filter.all`, { defaultValue: '全部线路' }),
            },
            {
              value: 'stable',
              label: t(`${ns}.filter.stable`, { defaultValue: '仅稳定' }),
            },
            {
              value: 'eco',
              label: t(`${ns}.filter.eco`, { defaultValue: '仅 Agent 专线' }),
            },
          ]}
        />
      </div>

      {view === 'provider' && summary && !health.isError ? (
        <div className='chealth-tiles'>
          <div className='chealth-tile'>
            <span className='chealth-tile__value'>
              {formatPercent(summary.stable_success_rate)}
            </span>
            <span className='chealth-tile__label'>
              {t(`${ns}.tile.stableShort`, { defaultValue: '稳定线路' })}
            </span>
          </div>
          <div className='chealth-tile'>
            <span
              className={
                summary.eco_success_rate != null &&
                summary.eco_success_rate < 0.99
                  ? 'chealth-tile__value chealth-tile__value--warn'
                  : 'chealth-tile__value'
              }
            >
              {formatPercent(summary.eco_success_rate)}
            </span>
            <span className='chealth-tile__label'>
              {t(`${ns}.tile.ecoShort`, { defaultValue: 'Agent 专线' })}
            </span>
          </div>
          <div className='chealth-tile'>
            <span
              className={
                summary.active_issues > 0
                  ? 'chealth-tile__value chealth-tile__value--warn'
                  : 'chealth-tile__value'
              }
            >
              {summary.active_issues}
            </span>
            <span className='chealth-tile__label'>
              {t(`${ns}.tile.issues`, { defaultValue: '活跃异常' })}
            </span>
          </div>
          {isAdmin && summary.provider_count ? (
            <div className='chealth-tile'>
              <span className='chealth-tile__value'>
                {summary.provider_count}
                {summary.provider_outage_count ||
                summary.provider_degraded_count ? (
                  <span className='chealth-tile__value-note'>
                    {` · ${summary.provider_outage_count || 0} / ${summary.provider_degraded_count || 0}`}
                  </span>
                ) : null}
              </span>
              <span className='chealth-tile__label'>
                {summary.provider_outage_count ||
                summary.provider_degraded_count
                  ? t(`${ns}.tile.providersWithIssues`, {
                      defaultValue: '供应商 · 故障 / 降级',
                    })
                  : t(`${ns}.tile.providers`, { defaultValue: '供应商' })}
              </span>
            </div>
          ) : null}
          <div className='chealth-tile'>
            <span className='chealth-tile__value'>
              {formatTps(summary.output_tps)}
            </span>
            <span className='chealth-tile__label'>
              {t(`${ns}.tile.tps`, { defaultValue: '输出 TPS' })}
            </span>
          </div>
        </div>
      ) : null}

      {health.isLoading ? <LoadingState /> : null}

      {health.isError ? (
        <ErrorState
          error={health.error}
          action={
            <Button
              loading={health.isFetching}
              onClick={() => void health.refetch()}
            >
              {t('common.retry', { defaultValue: '重试' })}
            </Button>
          }
          description={t(`${ns}.error.description`, {
            defaultValue:
              '读不到状态数据。这不代表线路有问题——只代表我们此刻看不到它的状态。',
          })}
          title={t(`${ns}.error.title`, { defaultValue: '状态读取失败' })}
        />
      ) : null}

      {!health.isLoading &&
      !health.isError &&
      (view === 'provider' ? !visibleProviders.length : !rows.length) ? (
        <EmptyState
          description={t(`${ns}.empty.description`, {
            defaultValue: '当前筛选条件下没有数据。',
          })}
          icon={<Boxes size={24} />}
          title={t(`${ns}.empty.title`, { defaultValue: '暂时没有数据' })}
        />
      ) : null}

      {view === 'provider' && visibleProviders.length && !health.isError ? (
        <div className='chealth-layout'>
          <div className='chealth-providers'>
            {visibleProviders.map((provider) => {
              const models = [
                ...new Set(provider.lanes.map((lane) => lane.model_name)),
              ];
              // A card grid only reads as a comparison while the cards are
              // comparable. One upstream serving twenty models would otherwise
              // set the height for its whole row, so the list is capped and
              // the remainder counted; the panel lists every route in full.
              const shownModels = models.slice(0, MODEL_CHIP_LIMIT);
              const hiddenModelCount = models.length - shownModels.length;
              const lanes = [
                ...new Set(provider.lanes.map((lane) => lane.lane)),
              ];
              const laneSummary =
                lanes.length === 1
                  ? laneLabel(lanes[0])
                  : t(`${ns}.provider.mixedLanes`, { defaultValue: '双通道' });
              return (
                <div
                  aria-pressed={
                    selectedProvider?.channel_id === provider.channel_id
                  }
                  className={`chealth-provider${
                    selectedProvider?.channel_id === provider.channel_id
                      ? ' chealth-provider--selected'
                      : ''
                  }`}
                  key={provider.channel_id}
                  onClick={() => setSelectedProviderId(provider.channel_id)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' || event.key === ' ') {
                      event.preventDefault();
                      setSelectedProviderId(provider.channel_id);
                    }
                  }}
                  role='button'
                  tabIndex={0}
                >
                  <span className='chealth-provider__head'>
                    <span className='chealth-provider__name'>
                      {provider.channel_name}
                    </span>
                    <Badge variant='neutral'>
                      {sourceTypeLabel(provider.source_type)}
                    </Badge>
                    <span
                      className={`chealth-provider__status ${statusToneClass(provider.status)}`}
                    >
                      <StatusMark status={provider.status} />
                      {statusLabel(provider.status)}
                    </span>
                  </span>

                  <span className='chealth-provider__figures'>
                    <span
                      className={`chealth-provider__rate ${statusToneClass(provider.status)}`}
                    >
                      {formatPercent(provider.success_rate)}
                    </span>
                    <span className='chealth-provider__meta'>
                      {t(`${ns}.provider.serves`, {
                        count: provider.model_count,
                        defaultValue: '{{count}} 模型',
                      })}
                      {` · ${laneSummary}`}
                    </span>
                    <span className='chealth-provider__p95'>
                      {`P95 ${formatMs(provider.p95_latency_ms)}`}
                    </span>
                  </span>

                  <HeatCells
                    buckets={
                      provider.buckets?.length
                        ? compactBuckets(provider.buckets, PROVIDER_CARD_SLICES)
                        : emptyBuckets
                    }
                    cellLabel={cellLabel}
                  />

                  <span className='chealth-provider__weight'>
                    {`w${provider.weight}`}
                    {provider.share != null
                      ? ` · ${t(`${ns}.provider.share`, {
                          defaultValue: '承载 {{share}}',
                          share: formatShare(provider.share),
                        })}`
                      : provider.probe_count > 0
                        ? ` · ${t(`${ns}.provider.probes`, {
                            count: provider.probe_count,
                            defaultValue: '探测 {{count}} 次',
                          })}`
                        : ''}
                  </span>

                  <span className='chealth-provider__models'>
                    {shownModels.map((name) => (
                      <span className='chealth-provider__model' key={name}>
                        {name}
                      </span>
                    ))}
                    {hiddenModelCount > 0 ? (
                      <span className='chealth-provider__model chealth-provider__model--more'>
                        {t(`${ns}.provider.moreModels`, {
                          count: hiddenModelCount,
                          defaultValue: '+{{count}}',
                        })}
                      </span>
                    ) : null}
                  </span>
                </div>
              );
            })}
          </div>

          {selectedProvider ? (
            <aside className='chealth-panel'>
              <div className='chealth-panel__title'>
                {selectedProvider.channel_name}
                <Badge variant={STATUS_VARIANT[selectedProvider.status]}>
                  {statusLabel(selectedProvider.status)}
                </Badge>
              </div>

              <p className='chealth-panel__note'>
                {selectedProvider.source === 'traffic'
                  ? t(`${ns}.provider.evidenceTraffic`, {
                      count: selectedProvider.request_count,
                      errors: selectedProvider.error_count,
                      models: selectedProvider.model_count,
                      defaultValue:
                        '{{count}} 笔真实请求实测，其中 {{errors}} 笔失败，覆盖它服务的 {{models}} 个模型。',
                    })
                  : selectedProvider.source === 'probe'
                    ? t(`${ns}.provider.evidenceProbe`, {
                        count: selectedProvider.probe_count,
                        defaultValue:
                          '本窗口无真实流量，由 {{count}} 次探测兜底：仅证明这个来源还能应答，不代表真实负载下的表现。',
                      })
                    : t(`${ns}.provider.evidenceNone`, {
                        defaultValue:
                          '本窗口既无真实流量也无探测记录，这个来源的状态未知。',
                      })}
              </p>

              <div>
                <div className='chealth-panel__section-label'>
                  {t(`${ns}.provider.routes`, {
                    defaultValue: '承载的通道 · 份额 · 成功率',
                  })}
                </div>
                <div className='chealth-pool'>
                  {selectedProvider.lanes.map((lane) => (
                    <button
                      className={`chealth-pool__row chealth-pool__row--link${
                        lane.disabled ? ' chealth-pool__row--disabled' : ''
                      }`}
                      key={`${lane.model_name}::${lane.lane}`}
                      onClick={() => {
                        // Jump to the same route in the by-model view: the
                        // operator has decided which lane to look at, and the
                        // matrix is where a lane's history lives.
                        // The kind filter is cleared with it, or a filter
                        // left on another section would hide the very route
                        // the button names.
                        setSearch(lane.model_name);
                        const params = new URLSearchParams(searchParams);
                        params.delete('view');
                        params.delete('kind');
                        setSearchParams(params, { replace: true });
                      }}
                      type='button'
                    >
                      <i
                        className={`chealth-lane-dot chealth-lane-dot--${lane.lane}`}
                      />
                      <span className='chealth-pool__name'>
                        {lane.model_name}
                      </span>
                      <span className='chealth-pool__meta'>
                        {formatShare(lane.share)}
                      </span>
                      <Badge variant={STATUS_VARIANT[lane.status]}>
                        {lane.source === 'none'
                          ? t(`${ns}.source.none`, { defaultValue: '无数据' })
                          : lane.source === 'probe'
                            ? t(`${ns}.source.probe`, { defaultValue: '探测' })
                            : formatPercent(lane.success_rate)}
                      </Badge>
                    </button>
                  ))}
                </div>
                <p className='chealth-panel__hint'>
                  {t(`${ns}.provider.jumpHint`, {
                    defaultValue: '点一条通道跳到按模型视图。',
                  })}
                </p>
              </div>

              <div className='chealth-panel__actions'>
                <Button
                  disabled={selectedProvider.disabled}
                  onClick={() => setPauseTarget(selectedProvider)}
                  variant='primary'
                >
                  {selectedProvider.disabled
                    ? t(`${ns}.provider.paused`, { defaultValue: '来源已停用' })
                    : t(`${ns}.provider.pause`, {
                        defaultValue: '停用这个来源',
                      })}
                </Button>
                <Button
                  disabled={!liveLanes(selectedProvider).length}
                  onClick={() => setFloorTarget(selectedProvider)}
                  variant='secondary'
                >
                  {t(`${ns}.provider.floor`, {
                    defaultValue: '把它的权重降到最低',
                  })}
                </Button>
                <Button
                  loading={testingId === selectedProvider.channel_id}
                  onClick={() => void runTest(selectedProvider)}
                  variant='secondary'
                >
                  {t(`${ns}.provider.test`, { defaultValue: '测试这个来源' })}
                </Button>
              </div>

              {selectedProvider.last_probe ? (
                <div className='chealth-panel__probe'>
                  {t(`${ns}.provider.lastProbe`, {
                    defaultValue: '最近一次探测 {{time}}',
                    time: new Date(
                      selectedProvider.last_probe.probed_at,
                    ).toLocaleTimeString(),
                  })}
                  {` · ${
                    selectedProvider.last_probe.success
                      ? t('common.status.success', { defaultValue: '成功' })
                      : selectedProvider.last_probe.error ||
                        t(`${ns}.provider.probeFailed`, {
                          defaultValue: '失败',
                        })
                  }`}
                  {selectedProvider.last_probe.latency_ms
                    ? ` · ${formatMs(selectedProvider.last_probe.latency_ms)}`
                    : ''}
                </div>
              ) : null}
            </aside>
          ) : null}
        </div>
      ) : null}

      {view === 'model' && rows.length > 0 && !health.isError && payload ? (
        <StatusBoard
          allRows={payload.rows}
          bucketSeconds={payload.bucket_seconds}
          isAdmin={isAdmin}
          kindLabel={kindLabel}
          laneLabel={laneLabel}
          rows={rows}
          sections={sections}
          sourceLabel={sourceLabel}
          windowSeconds={payload.window_seconds}
        />
      ) : null}

      <Modal
        busy={pausing}
        description={t(`${ns}.pause.description`, {
          defaultValue:
            '停用后这个来源会退出它服务的每个池子，流量交给池里其余来源。历史数据保留，随时可以重新启用。',
        })}
        footer={
          <>
            <Button
              disabled={pausing}
              onClick={() => setPauseTarget(null)}
              variant='secondary'
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              loading={pausing}
              onClick={() => void runPause()}
              variant='danger'
            >
              {t(`${ns}.pause.confirm`, { defaultValue: '确认停用' })}
            </Button>
          </>
        }
        onOpenChange={(open) => {
          if (!open) setPauseTarget(null);
        }}
        open={Boolean(pauseTarget)}
        title={t(`${ns}.pause.title`, {
          defaultValue: '停用 {{name}}',
          name: pauseTarget?.channel_name ?? '',
        })}
        body={
          pauseTarget ? (
            <div className='chealth-pool'>
              {liveLanes(pauseTarget).map((lane: ChannelHealthProviderLane) => (
                <div
                  className='chealth-pool__row'
                  key={`${lane.model_name}::${lane.lane}`}
                >
                  <i
                    className={`chealth-lane-dot chealth-lane-dot--${lane.lane}`}
                  />
                  <span className='chealth-pool__name'>{lane.model_name}</span>
                  <span className='chealth-pool__meta'>
                    {laneLabel(lane.lane)}
                  </span>
                </div>
              ))}
            </div>
          ) : null
        }
      />

      <Modal
        busy={flooring}
        description={t(`${ns}.floor.description`, {
          defaultValue:
            '把这个来源在下列通道上的权重设为 0。注意它不会停止接流量——路由给每条在线通道一个基础权重，所以权重 0 只是把它降到池子里的最低份额。要完全停掉，用「停用这个来源」。',
        })}
        footer={
          <>
            <Button
              disabled={flooring}
              onClick={() => setFloorTarget(null)}
              variant='secondary'
            >
              {t('common.cancel', { defaultValue: '取消' })}
            </Button>
            <Button
              loading={flooring}
              onClick={() => void runFloor()}
              variant='primary'
            >
              {t(`${ns}.floor.confirm`, { defaultValue: '确认降权' })}
            </Button>
          </>
        }
        onOpenChange={(open) => {
          if (!open) setFloorTarget(null);
        }}
        open={Boolean(floorTarget)}
        title={t(`${ns}.floor.title`, {
          defaultValue: '把 {{name}} 的权重降到最低',
          name: floorTarget?.channel_name ?? '',
        })}
        body={
          floorTarget ? (
            <div className='chealth-pool'>
              {liveLanes(floorTarget).map((lane: ChannelHealthProviderLane) => (
                <div
                  className='chealth-pool__row'
                  key={`${lane.model_name}::${lane.lane}`}
                >
                  <i
                    className={`chealth-lane-dot chealth-lane-dot--${lane.lane}`}
                  />
                  <span className='chealth-pool__name'>{lane.model_name}</span>
                  <span className='chealth-pool__meta'>
                    {`${laneLabel(lane.lane)} · w${lane.weight} → w0`}
                  </span>
                </div>
              ))}
            </div>
          ) : null
        }
      />
    </ConsolePage>
  );
}

export default ChannelHealthPage;

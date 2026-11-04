import { AlertTriangle, MoreVertical, Pencil, Trash2, Zap } from 'lucide-react';
import {
  formatLatency,
  formatPerMtok,
  formatRate,
  healthTone,
} from './formatters';
import { ModelOpsBars } from './healthBars';
import { observationSummaryLine } from './ObservationDialog';
import SourceTypeBadge from './SourceTypeBadge';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../../components/ui/dropdown-menu';
import { IconButton } from '../../components/ui/icon-button';
import { resolveProviderBrand } from '../../components/ui/provider-brands';
import { ProviderLogo } from '../../components/ui/provider-icons';
import { Switch } from '../../components/ui/switch';
import { useChannelLanesQuery } from '../../hooks/models/useModelOpsDimensions';
import type { ModelOpsSource } from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';
import '../../components/ui/row-actions.css';

const ns = 'page.console.modelOps';

/* A click that landed on a control inside the card belongs to that control.
   Guarding here keeps the cells free of stopPropagation wrappers. */
const fromControl = (event: React.MouseEvent | React.KeyboardEvent) =>
  Boolean(
    (event.target as HTMLElement | null)?.closest(
      'a, button, input, label, [role="switch"], [role="menu"], [role="menuitem"]',
    ),
  );

/** A source whose every route is still being watched: it carries no traffic
 *  at all yet, so its card shows the watch instead of a live record. */
export const isObservingSource = (source: ModelOpsSource) =>
  Boolean(source.observation && !source.observation.live_routes);

/** The upstream's display name. A type the console has no name for arrives as
 *  its bare number, which reads as a count next to "12 个模型". */
export const sourceTypeName = (source: ModelOpsSource, t: TFunction) => {
  const name = String(source.type_name || '').trim();
  if (name && !/^\d+$/.test(name)) return name;
  return t(`${ns}.sources.unknownType`, {
    defaultValue: '未知类型 {{type}}',
    type: name || source.type,
  });
};

/** 成本: every figure carries its unit, so a per-call price never reads as a
 *  per-million one. The first kind fits the card; all of them go in the title. */
const costParts = (source: ModelOpsSource, t: TFunction) =>
  [
    source.cost_input_per_mtok !== null || source.cost_output_per_mtok !== null
      ? `$${formatPerMtok(source.cost_input_per_mtok)} / $${formatPerMtok(source.cost_output_per_mtok)}`
      : null,
    source.cost_request_usd != null
      ? `$${formatPerMtok(source.cost_request_usd)} ${t(`${ns}.tier.perCallUnit`, { defaultValue: '/ 次' })}`
      : null,
    source.cost_second_usd != null
      ? `$${formatPerMtok(source.cost_second_usd)} ${t(`${ns}.tier.perSecondUnit`, { defaultValue: '/ 秒' })}`
      : null,
  ].filter((part): part is string => Boolean(part));

type SourceLedgerProps = {
  /** A source deleted a moment ago: the card reads 删除中 until the undo
   *  countdown ends, and offers nothing to click. */
  isUndoPending?: (source: ModelOpsSource) => boolean;
  onDelete: (source: ModelOpsSource) => void;
  onOpenObservation: (source: ModelOpsSource) => void;
  onSelect: (id: string | null) => void;
  onTest: (source: ModelOpsSource) => void;
  onToggle: (source: ModelOpsSource, enabled: boolean) => void;
  selectedId: string | null;
  sources: ModelOpsSource[];
  t: TFunction;
  windowHours: number;
};

/** 来源 — one card per source. A card is name and type over the window's
 *  record, the cost, and one control for whether it serves; editing opens
 *  from the card itself, the rest sits behind ⋯. */
export default function SourceLedger({
  isUndoPending,
  onDelete,
  onOpenObservation,
  onSelect,
  onTest,
  onToggle,
  selectedId,
  sources,
  t,
  windowHours,
}: SourceLedgerProps) {
  /* The lane chip reads its name and colour from the registry. The first
     lane is the default and goes unlabelled; the header switch filters by
     lane, so the card only flags a source that sits somewhere else. */
  const lanes = useChannelLanesQuery().data ?? [];
  const defaultLane = lanes[0]?.slug;
  const laneOf = (slug?: string) => lanes.find((lane) => lane.slug === slug);

  return (
    <div
      aria-label={t(`${ns}.sources.colName`, { defaultValue: '来源' })}
      className='mo-cards'
      role='table'
    >
      {sources.map((source) => {
        const id = String(source.id);
        const isSelected = selectedId === id;
        const tone = healthTone(source.health, source.status);
        const allBuckets = source.health.buckets || [];
        const measured = allBuckets.some(
          (bucket) => bucket.success_rate !== null,
        );
        const observation = source.observation;
        const observing = isObservingSource(source);
        const deleting = Boolean(isUndoPending?.(source));
        const buckets = measured ? allBuckets : [];
        const cost = costParts(source, t);
        const lane = laneOf(source.lane);
        const showLane =
          lanes.length > 1 && source.lane && source.lane !== defaultLane;
        const summary = observation?.summary;
        const summaryParts = summary
          ? (observationSummaryLine(summary, t) ?? [])
          : [];
        const progress = observation
          ? observation.complete
            ? 1
            : Math.min(1, observation.day / (observation.days_required || 1))
          : 0;

        return (
          <div
            aria-label={
              observing
                ? t(`${ns}.sources.cardObserving`, {
                    defaultValue: '{{name}}，观察中',
                    name: source.name,
                  })
                : source.name
            }
            aria-selected={isSelected}
            className={`mo-card${isSelected ? ' is-selected' : ''}${observing ? ' is-observing' : ''}${deleting ? ' is-undo-pending' : ''}`}
            key={id}
            role='row'
            tabIndex={deleting ? -1 : 0}
            onClick={(event) => {
              if (deleting || fromControl(event)) return;
              onSelect(id);
            }}
            onKeyDown={(event) => {
              if (deleting || fromControl(event)) return;
              if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault();
                onSelect(id);
              }
            }}
          >
            <span className='mo-card__logo' role='cell'>
              <ProviderLogo
                brand={resolveProviderBrand({
                  name: source.type_name || source.name,
                })}
                decorative
                size={16}
              />
            </span>

            <span className='mo-card__main' role='cell'>
              <span className='mo-card__title'>
                <span className='mo-card__name'>{source.name}</span>
                {observing ? (
                  <span className='mo-card__watch'>
                    {t(`${ns}.sources.observing`, { defaultValue: '观察中' })}
                  </span>
                ) : null}
              </span>
              <span className='mo-card__meta'>
                <SourceTypeBadge t={t} value={source.source_type} />
                {showLane ? (
                  <span className='mo-card__lane'>
                    <span
                      className={`mo-dot mo-dot--lane-${lane?.color || 'info'}`}
                    />
                    {lane?.name || source.lane}
                  </span>
                ) : null}
                {observing && observation ? (
                  <>
                    <span
                      aria-label={t(`${ns}.sources.observeProgress`, {
                        day: observation.complete
                          ? observation.days_required
                          : observation.day,
                        defaultValue: '观察进度 {{day}}/{{total}} 天',
                        total: observation.days_required,
                      })}
                      className='mo-card__progress'
                      role='img'
                    >
                      <span className='mo-card__progress-track'>
                        <span
                          className='mo-card__progress-fill'
                          style={{ width: `${Math.round(progress * 100)}%` }}
                        />
                      </span>
                      <span aria-hidden='true'>
                        {t(`${ns}.sources.observeDays`, {
                          day: observation.complete
                            ? observation.days_required
                            : observation.day,
                          defaultValue: '{{day}}/{{total}} 天',
                          total: observation.days_required,
                        })}
                      </span>
                    </span>
                    {summary && summary.problem_count > 0 ? (
                      <span
                        className='mo-card__problems'
                        title={summaryParts
                          .filter((part) => part.tone === 'bad')
                          .map((part) => part.text)
                          .join(' · ')}
                      >
                        <AlertTriangle aria-hidden='true' size={12} />
                        {t(`${ns}.sources.problems`, {
                          count: summary.problem_count,
                          defaultValue: '{{count}} 个有问题',
                        })}
                      </span>
                    ) : summaryParts.length ? (
                      <span className='mo-card__sub'>
                        {summaryParts.map((part) => part.text).join(' · ')}
                      </span>
                    ) : null}
                  </>
                ) : (
                  <span className='mo-card__sub'>
                    {t(`${ns}.sources.serves`, {
                      count: source.model_count,
                      defaultValue: '{{typeName}} · {{count}} 个模型',
                      typeName: sourceTypeName(source, t),
                    })}
                    {observation && !deleting ? (
                      <>
                        {' · '}
                        <Button
                          className='mo-obs-link'
                          size='xs'
                          variant='link'
                          onClick={() => onOpenObservation(source)}
                        >
                          {t(`${ns}.sources.someObserving`, {
                            count: observation.models ?? 0,
                            defaultValue: '{{count}} 个观察中',
                          })}
                        </Button>
                      </>
                    ) : null}
                  </span>
                )}
              </span>
            </span>

            <span className='mo-card__health' role='cell'>
              {/* No measurement says so in words. A row of grey bars would
                  read as a chart still loading. */}
              {buckets.length === 0 ? (
                <span className='mo-card__sub'>
                  {t(`${ns}.sources.notMeasured`, {
                    defaultValue: '尚未观测',
                  })}
                </span>
              ) : (
                <>
                  <span
                    className={`mo-card__bars${source.health.source === 'probe' ? ' is-probe' : ''}`}
                  >
                    <ModelOpsBars
                      buckets={buckets}
                      height={14}
                      label={t(`${ns}.sources.probeTitle`, {
                        defaultValue: '{{hours}} 小时内按等分时段的成功率',
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
                    <span className={`mo-card__rate is-${tone}`}>
                      {source.health.request_count > 0
                        ? formatRate(source.health.success_rate)
                        : '—'}
                    </span>
                  </span>
                  <span className='mo-card__figures'>
                    {`${windowHours}h · `}
                    {observing
                      ? t(`${ns}.sources.probeShort`, {
                          defaultValue: '抽测 {{ok}}/{{total}}',
                          ok: (
                            source.health.request_count -
                            source.health.error_count
                          ).toLocaleString(),
                          total: source.health.request_count.toLocaleString(),
                        })
                      : source.health.request_count > 0
                        ? t(`${ns}.sources.healthFigures`, {
                            defaultValue: '均值 {{latency}} · {{requests}} 次',
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
                </>
              )}
            </span>

            <span
              className='mo-card__cost'
              title={cost.join(' · ') || undefined}
            >
              <span
                className={`mo-card__cost-value${cost.length ? '' : ' is-empty'}`}
              >
                {cost[0] || '—'}
              </span>
              <span className='mo-card__cost-label'>
                {source.cost_input_per_mtok !== null ||
                source.cost_output_per_mtok !== null
                  ? t(`${ns}.sources.costPerM`, { defaultValue: '成本 $/M' })
                  : t(`${ns}.sources.colCost`, { defaultValue: '成本' })}
              </span>
            </span>

            {/* A watched source is not switched on; it goes live, so the
                check takes the switch's place. */}
            {deleting ? (
              <span className='mo-card__state mo-card__state--wide' role='cell'>
                <Badge variant='neutral'>
                  {t('undo.deleting', { defaultValue: '删除中' })}
                </Badge>
              </span>
            ) : (
              <>
                <span className='mo-card__state' role='cell'>
                  {observing && observation ? (
                    <Button
                      size='sm'
                      variant='secondary'
                      onClick={() => onOpenObservation(source)}
                    >
                      {t(`${ns}.sources.goLiveCheck`, {
                        count: observation.models ?? 0,
                        defaultValue: '上线检查 · {{count}}',
                      })}
                    </Button>
                  ) : (
                    <Switch
                      aria-label={t(`${ns}.sources.toggle`, {
                        defaultValue: '启用 {{name}}',
                        name: source.name,
                      })}
                      checked={source.status === 1}
                      onCheckedChange={(checked: boolean) =>
                        onToggle(source, checked)
                      }
                    />
                  )}
                </span>
                <span className='mo-card__more' role='cell'>
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
                        <MoreVertical size={16} />
                      </IconButton>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align='end' className='min-w-[160px]'>
                      <DropdownMenuItem onSelect={() => onSelect(id)}>
                        <span className='lazu-dropdown-item__icon'>
                          <Pencil size={14} />
                        </span>
                        {t('page.console.tables.edit', {
                          defaultValue: '编辑',
                        })}
                      </DropdownMenuItem>
                      <DropdownMenuItem onSelect={() => onTest(source)}>
                        <span className='lazu-dropdown-item__icon'>
                          <Zap size={14} />
                        </span>
                        {t(`${ns}.sources.test`, { defaultValue: '测试' })}
                      </DropdownMenuItem>
                      <div
                        className='lazu-dropdown-separator'
                        role='separator'
                      />
                      <DropdownMenuItem
                        className='lazu-dropdown-item--danger'
                        onSelect={() => onDelete(source)}
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
                </span>
              </>
            )}
          </div>
        );
      })}
    </div>
  );
}

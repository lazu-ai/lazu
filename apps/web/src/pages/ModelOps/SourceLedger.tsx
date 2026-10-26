import { Pencil, Trash2 } from 'lucide-react';
import {
  formatLatency,
  formatPerMtok,
  formatRate,
  healthTone,
} from './formatters';
import { ModelOpsBars } from './healthBars';
import { observationProgress } from './observation';
import { observationSummaryLine, shortModelName } from './ObservationDialog';
import SourceTypeBadge from './SourceTypeBadge';
import { Button } from '../../components/ui/button';
import { resolveProviderBrand } from '../../components/ui/provider-brands';
import { ProviderLogo } from '../../components/ui/provider-icons';
import { Switch } from '../../components/ui/switch';
import { useChannelLanesQuery } from '../../hooks/models/useModelOpsDimensions';
import type { ModelOpsSource } from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps';

const COLS_FULL = '48px minmax(0, 1fr) 96px 96px 124px 232px 120px 176px';
const COLS_NARROW = '48px minmax(0, 1fr) 110px 112px 84px';

/* A click that landed on a control inside the row belongs to that control.
   Guarding here keeps the cells free of stopPropagation wrappers, which are
   click handlers on non-interactive elements as far as a11y is concerned. */
const fromControl = (event: React.MouseEvent | React.KeyboardEvent) =>
  Boolean(
    (event.target as HTMLElement | null)?.closest(
      'a, button, input, label, [role="switch"], [role="checkbox"], [role="menu"]',
    ),
  );

const TONE_COLOR: Record<string, string> = {
  bad: 'var(--lazu-danger)',
  idle: 'var(--lazu-border-strong)',
  ok: 'var(--lazu-success)',
  warn: 'var(--lazu-warning)',
};

/** A source whose every route is still being watched: it carries no traffic
 *  at all yet, so its row shows the watch instead of a live record. */
export const isObservingSource = (source: ModelOpsSource) =>
  Boolean(source.observation && !source.observation.live_routes);

type SourceLedgerProps = {
  onDelete: (source: ModelOpsSource) => void;
  onOpenObservation: (source: ModelOpsSource) => void;
  onSelect: (id: string | null) => void;
  onTest: (source: ModelOpsSource) => void;
  onToggle: (source: ModelOpsSource, enabled: boolean) => void;
  narrow: boolean;
  selectedId: string | null;
  sources: ModelOpsSource[];
  t: TFunction;
  windowHours: number;
};

/** 来源账簿 — the same grid as 模型, so the two pages read as one object. */
export default function SourceLedger({
  narrow,
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
  /* The lane chip reads its name and colour from the registry: with lanes
     user-managed, a hard-coded 稳定 / 便宜 pair would mislabel every row that
     an operator moved into a lane they added. */
  const lanes = useChannelLanesQuery().data ?? [];
  const laneOf = (slug?: string) => lanes.find((lane) => lane.slug === slug);
  const laneName = (slug?: string) => laneOf(slug)?.name || slug || '';
  const laneColor = (slug?: string) => laneOf(slug)?.color || 'info';

  return (
    <div
      className={`mo-ledger${narrow ? ' is-narrow' : ''}`}
      role='table'
      style={
        {
          '--cols': narrow ? COLS_NARROW : COLS_FULL,
          '--row-h': '60px',
        } as React.CSSProperties
      }
    >
      <div className='mo-ledger__head' role='row'>
        <span role='columnheader' />
        <span role='columnheader'>
          {t(`${ns}.sources.colName`, { defaultValue: '来源' })}
        </span>
        <span role='columnheader'>
          {t(`${ns}.sources.colType`, { defaultValue: '类型' })}
        </span>
        <span className='mo-col-optional mo-col-compact' role='columnheader'>
          {t(`${ns}.sources.colLane`, { defaultValue: '供货档位' })}
        </span>
        <span
          className='mo-col-optional mo-col-compact'
          role='columnheader'
          style={{ textAlign: 'right' }}
          title='$/M'
        >
          {t(`${ns}.sources.colCost`, { defaultValue: '成本基准' })}
        </span>
        <span className='mo-col-optional mo-col-compact' role='columnheader'>
          {t(`${ns}.sources.colStability`, {
            defaultValue: '{{hours}}h 稳定性',
            hours: windowHours,
          })}
        </span>
        <span role='columnheader'>
          {t(`${ns}.sources.colState`, { defaultValue: '状态' })}
        </span>
        <span role='columnheader' />
      </div>

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
        const buckets = measured ? allBuckets : [];
        const cost =
          [
            source.cost_input_per_mtok !== null ||
            source.cost_output_per_mtok !== null
              ? `${formatPerMtok(source.cost_input_per_mtok)} / ${formatPerMtok(source.cost_output_per_mtok)}`
              : null,
            source.cost_request_usd != null
              ? `$${formatPerMtok(source.cost_request_usd)}${t(`${ns}.tier.perCallUnit`, { defaultValue: '/ 次' })}`
              : null,
            source.cost_second_usd != null
              ? `$${formatPerMtok(source.cost_second_usd)}${t(`${ns}.tier.perSecondUnit`, { defaultValue: '/ 秒' })}`
              : null,
          ]
            .filter(Boolean)
            .join(' · ') || null;

        return (
          <div
            className={`mo-ledger__row${isSelected ? ' is-selected' : ''}${observing ? ' is-observing' : ''}`}
            key={id}
            aria-selected={isSelected}
            role='row'
            tabIndex={0}
            onClick={(event) => {
              if (fromControl(event)) return;
              onSelect(id);
            }}
            onKeyDown={(event) => {
              if (fromControl(event)) return;
              if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault();
                onSelect(id);
              }
            }}
          >
            <span role='cell'>
              <ProviderLogo
                brand={resolveProviderBrand({
                  name: source.type_name || source.name,
                })}
                decorative
                size={20}
              />
            </span>
            <span role='cell'>
              <span className='mo-name' style={{ display: 'block' }}>
                {source.name}
              </span>
              {observing && observation ? (
                <span
                  className='mo-sub mo-sub--line'
                  style={{ display: 'block' }}
                >
                  {t(`${ns}.sources.observing`, { defaultValue: '观察中' })}
                  {' · '}
                  <span className='mo-sub__days'>
                    {observationProgress(observation, t)}
                  </span>
                  {' · '}
                  {t(`${ns}.sources.observingModels`, {
                    count: observation.models ?? 0,
                    defaultValue: '{{count}} 个模型',
                  })}
                </span>
              ) : (
                <span
                  className='mo-sub mo-sub--line'
                  style={{ display: 'block' }}
                >
                  {t(`${ns}.sources.serves`, {
                    count: source.model_count,
                    defaultValue: '{{typeName}} · {{count}} 个模型',
                    typeName: source.type_name || source.type,
                  })}
                  {observation ? (
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
              {observing && observation?.summary ? (
                <span
                  className='mo-obs-line'
                  title={(observationSummaryLine(observation.summary, t) ?? [])
                    .map((part) => part.text)
                    .join(' · ')}
                >
                  {(observationSummaryLine(observation.summary, t) ?? []).map(
                    (part, index) => (
                      <span key={part.text}>
                        {index > 0 ? ' · ' : ''}
                        <span className={`mo-obs-line__part is-${part.tone}`}>
                          {part.text}
                        </span>
                      </span>
                    ),
                  )}
                </span>
              ) : null}
            </span>
            <span role='cell'>
              <SourceTypeBadge t={t} value={source.source_type} />
            </span>
            <span className='mo-col-optional mo-col-compact' role='cell'>
              <span className='mo-chip'>
                <span
                  className={`mo-dot mo-dot--lane-${laneColor(source.lane)}`}
                />
                {laneName(source.lane)}
              </span>
            </span>
            <span
              className='mo-col-optional mo-col-compact mo-num mo-mono'
              role='cell'
              style={{ textAlign: 'right' }}
            >
              {cost || (
                <span style={{ color: 'var(--lazu-text-faint)' }}>—</span>
              )}
            </span>
            <span className='mo-col-optional mo-col-compact' role='cell'>
              {/* No measurement says so in words. A row of grey bars would read
                  as a chart still loading. */}
              {buckets.length === 0 ? (
                <span className='mo-sub'>
                  {t(`${ns}.sources.notMeasured`, {
                    defaultValue: '尚未观测',
                  })}
                </span>
              ) : (
                <span className='mo-health'>
                  <span className='mo-health__line'>
                    <ModelOpsBars
                      buckets={buckets}
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
                    <span
                      className='mo-health__uptime'
                      style={{
                        color: tone === 'ok' ? undefined : TONE_COLOR[tone],
                      }}
                    >
                      {source.health.request_count > 0
                        ? formatRate(source.health.success_rate)
                        : '—'}
                    </span>
                  </span>
                  {observing && observation ? (
                    <span className='mo-health__figures'>
                      {t(`${ns}.sources.probeFigures`, {
                        defaultValue: '近 {{hours}}h 抽测 {{ok}}/{{total}}',
                        hours: windowHours,
                        ok: (
                          source.health.request_count -
                          source.health.error_count
                        ).toLocaleString(),
                        total: source.health.request_count.toLocaleString(),
                      })}
                      {observation.summary?.worst_probe
                        ? t(`${ns}.sources.probeWorst`, {
                            defaultValue: '（{{model}} {{ok}}/{{total}}）',
                            model: shortModelName(
                              observation.summary.worst_probe.model,
                            ),
                            ok:
                              observation.summary.worst_probe.samples -
                              observation.summary.worst_probe.failures,
                            total: observation.summary.worst_probe.samples,
                          })
                        : null}
                    </span>
                  ) : (
                    <span className='mo-health__figures'>
                      {source.health.request_count > 0
                        ? t(`${ns}.sources.healthFigures`, {
                            defaultValue: '均值 {{latency}} · {{requests}} 次',
                            requests:
                              source.health.request_count.toLocaleString(),
                            latency: formatLatency(
                              source.health.avg_latency_ms,
                            ),
                          })
                        : t(`${ns}.sources.idleWindow`, {
                            defaultValue: '窗口内无流量',
                          })}
                    </span>
                  )}
                </span>
              )}
            </span>
            <span role='cell'>
              {/* A watched source is not switched on; it goes live. The two
                  controls share the column because they answer the same
                  question: is this source serving? */}
              {observing && observation ? (
                <Button
                  size='sm'
                  variant='primary'
                  onClick={() => onOpenObservation(source)}
                >
                  {t(`${ns}.sources.viewObserving`, {
                    count: observation.models ?? 0,
                    defaultValue: '查看 {{count}} 个',
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
            <span className='mo-rowbtns' role='cell'>
              <Button
                aria-label={t('page.console.tables.edit')}
                aria-pressed={isSelected}
                className={`mo-rowbtn${isSelected ? ' is-open' : ''}`}
                size='xs'
                title={t('page.console.tables.edit')}
                variant='secondary'
                onClick={() => onSelect(id)}
              >
                <Pencil size={13} />
                {narrow ? null : t('page.console.tables.edit')}
              </Button>
              <Button
                aria-label={t('page.console.tables.delete')}
                className='mo-rowbtn mo-rowbtn--danger'
                size='xs'
                title={t('page.console.tables.delete')}
                variant='secondary'
                onClick={() => onDelete(source)}
              >
                <Trash2 size={13} />
                {narrow ? null : t('page.console.tables.delete')}
              </Button>
            </span>
          </div>
        );
      })}
    </div>
  );
}

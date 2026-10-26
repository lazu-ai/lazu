import { formatLatency, formatRate, healthTone } from './formatters';
import { ModelOpsBars } from './healthBars';
import { ObservationStrip } from './observation';
import SourceTypeBadge from './SourceTypeBadge';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import type { ModelOpsPoolEntry } from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps';

type LanePoolProps = {
  onGoLive: (entry: ModelOpsPoolEntry) => void;
  onPromote: (entry: ModelOpsPoolEntry) => void;
  onWeightChange: (channelId: string, value: string) => void;
  pool: ModelOpsPoolEntry[];
  t: TFunction;
  weights: Record<string, string>;
  windowHours: number;
};

/** The sources one lane of a model is served from: each with its record,
 *  its weight (a primary) or its role (a backup or a watched source). */
export default function LanePool({
  onGoLive,
  onPromote,
  onWeightChange,
  pool,
  t,
  weights,
  windowHours,
}: LanePoolProps) {
  const watched = pool.filter(
    (entry) => entry.role === 'inventory' && entry.observation,
  );
  const roleTag = (entry: ModelOpsPoolEntry) =>
    entry.role === 'backup'
      ? t(`${ns}.tier.roleBackup`, { defaultValue: '备用' })
      : entry.observation
        ? t(`${ns}.tier.roleObserving`, { defaultValue: '观察中' })
        : t(`${ns}.tier.roleInventory`, { defaultValue: '库存' });

  return (
    <div className='mo-pool'>
      {pool.length === 0 ? (
        <p className='model-ops__hint'>
          {t(`${ns}.tier.emptyPool`, {
            defaultValue: '这条线路还没有来源。',
          })}
        </p>
      ) : (
        pool.map((entry) => {
          const entryTone = healthTone(entry.health, entry.channel_status);
          const probed = entry.health.source === 'probe';
          const observing = Boolean(
            entry.role === 'inventory' && entry.observation,
          );
          return (
            <div className='model-ops__src-line' key={entry.channel_id}>
              <SourceTypeBadge t={t} value={entry.source_type} />
              <span className='model-ops__mono model-ops__src-line-name'>
                {entry.name}
              </span>
              {entry.channel_status !== 1 ||
              !entry.health.request_count ||
              !entry.health.buckets?.length ? (
                <span className='model-ops__hint model-ops__src-line-idle'>
                  {entry.channel_status !== 1
                    ? t(`${ns}.tier.sourceDisabled`, { defaultValue: '已禁用' })
                    : t(`${ns}.tier.sourceIdle`, {
                        defaultValue: '窗口内无流量',
                      })}
                </span>
              ) : (
                <>
                  <ModelOpsBars
                    buckets={entry.health.buckets}
                    height={10}
                    label={t(`${ns}.tier.sourceRecordLabel`, {
                      defaultValue: '{{name}} 过去 {{hours}} 小时的记录',
                      hours: windowHours,
                      name: entry.name,
                    })}
                    probed={
                      probed ? (observing ? 'observing' : 'idle') : undefined
                    }
                  />
                  <span
                    className={`model-ops__src-line-figures model-ops__src-line-figures--${entryTone}`}
                  >
                    {probed && !observing
                      ? t(`${ns}.tier.probeFigures`, {
                          defaultValue: '探测 · {{latency}}',
                          latency: formatLatency(entry.health.avg_latency_ms),
                        })
                      : `${formatRate(entry.health.success_rate)} · ${formatLatency(entry.health.avg_latency_ms)}`}
                  </span>
                </>
              )}
              {entry.role && entry.role !== 'primary' ? (
                <span className='model-ops__src-line-role'>
                  {entry.role === 'backup' ? (
                    <Button
                      aria-label={t(`${ns}.tier.promoteFor`, {
                        defaultValue: '把 {{name}} 升为主用',
                        name: entry.name,
                      })}
                      size='xs'
                      variant='secondary'
                      onClick={() => onPromote(entry)}
                    >
                      {t(`${ns}.tier.promote`, { defaultValue: '升为主用' })}
                    </Button>
                  ) : null}
                  <span className='model-ops__role-tag'>{roleTag(entry)}</span>
                </span>
              ) : (
                <span className='model-ops__src-line-weight'>
                  {t(`${ns}.tier.weight`, { defaultValue: '权重' })}
                  <Input
                    aria-label={t(`${ns}.tier.weightFor`, {
                      defaultValue: '{{name}} 的权重',
                      name: entry.name,
                    })}
                    className='model-ops__weight-input'
                    inputMode='numeric'
                    value={weights[entry.channel_id] ?? String(entry.weight)}
                    onChange={(event) =>
                      onWeightChange(entry.channel_id, event.target.value)
                    }
                  />
                </span>
              )}
            </div>
          );
        })
      )}

      {watched.map((entry) =>
        entry.observation ? (
          <ObservationStrip
            disabledReason={
              entry.channel_status !== 1
                ? t(`${ns}.observation.sourceOff`, {
                    defaultValue: '来源已停用，观察暂停。',
                  })
                : undefined
            }
            key={`observe-${entry.channel_id}`}
            name={entry.name}
            observation={entry.observation}
            t={t}
            onGoLive={() => onGoLive(entry)}
          />
        ) : null,
      )}
    </div>
  );
}

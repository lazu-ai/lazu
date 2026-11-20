import { formatLatency, healthTone } from './formatters';
import { emptyBuckets, ModelOpsBars } from './healthBars';
import { ObservationStrip } from './observation';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import type { ModelOpsPoolEntry } from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps';

type LanePoolProps = {
  /** The card is in 改价: weights become fields and backups can be promoted. */
  editing: boolean;
  onGoLive: (entry: ModelOpsPoolEntry) => void;
  onPromote: (entry: ModelOpsPoolEntry) => void;
  onWeightChange: (channelId: string, value: string) => void;
  pool: ModelOpsPoolEntry[];
  t: TFunction;
  weights: Record<string, string>;
  windowHours: number;
};

/** The sources one lane is served from: a name and its state, then the
 *  window's record as thin bars with its role, weight and latency. */
export default function LanePool({
  editing,
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
  const roleName = (entry: ModelOpsPoolEntry) =>
    entry.role === 'backup'
      ? t(`${ns}.tier.roleBackup`, { defaultValue: '备用' })
      : entry.role === 'inventory'
        ? entry.observation
          ? t(`${ns}.tier.roleObserving`, { defaultValue: '观察中' })
          : t(`${ns}.tier.roleInventory`, { defaultValue: '库存' })
        : t(`${ns}.tier.rolePrimary`, { defaultValue: '主用' });

  const state = (entry: ModelOpsPoolEntry) => {
    if (entry.channel_status !== 1)
      return {
        label: t(`${ns}.tier.sourceDisabled`, { defaultValue: '已停用' }),
        tone: 'idle',
      };
    if (entry.role === 'inventory' && entry.observation)
      return {
        label: t(`${ns}.tier.roleObserving`, { defaultValue: '观察中' }),
        tone: 'warn',
      };
    const tone = healthTone(entry.health, entry.channel_status);
    return {
      label:
        tone === 'bad'
          ? t(`${ns}.tier.sourceBad`, { defaultValue: '异常' })
          : tone === 'warn'
            ? t(`${ns}.tier.sourceWarn`, { defaultValue: '波动' })
            : tone === 'idle'
              ? t(`${ns}.tier.sourceIdle`, { defaultValue: '窗口内无流量' })
              : t(`${ns}.tier.sourceOk`, { defaultValue: '正常' }),
      tone,
    };
  };

  return (
    <div className='mdl-pool'>
      <p className='mdl-cap mdl-pool__title'>
        {t(`${ns}.tier.sources`, { defaultValue: '来源' })}
      </p>
      {pool.length === 0 ? (
        <p className='mdl-pool__empty'>
          {t(`${ns}.tier.emptyPool`, {
            defaultValue: '这条线路还没有来源。',
          })}
        </p>
      ) : (
        pool.map((entry) => {
          const probed = entry.health.source === 'probe';
          const observing = Boolean(
            entry.role === 'inventory' && entry.observation,
          );
          const { label, tone } = state(entry);
          const buckets =
            entry.channel_status === 1 && entry.health.buckets?.length
              ? entry.health.buckets
              : emptyBuckets(entry.health.buckets?.length || 48);
          const primary = !entry.role || entry.role === 'primary';
          return (
            <div className='mdl-src' key={entry.channel_id}>
              <div className='mdl-src__head'>
                <span className='mdl-src__name'>{entry.name}</span>
                <span className='mdl-src__state'>
                  <span
                    aria-hidden='true'
                    className={`mdl-dot mdl-dot--${tone}`}
                  />
                  {label}
                </span>
              </div>
              <div className='mdl-src__line'>
                <span className='mdl-src__bars'>
                  <ModelOpsBars
                    buckets={buckets}
                    height={5}
                    label={t(`${ns}.tier.sourceRecordLabel`, {
                      defaultValue: '{{name}} 过去 {{hours}} 小时的记录',
                      hours: windowHours,
                      name: entry.name,
                    })}
                    probed={
                      probed ? (observing ? 'observing' : 'idle') : undefined
                    }
                  />
                </span>
                {editing && primary ? (
                  <span className='mdl-src__weight'>
                    {t(`${ns}.tier.weight`, { defaultValue: '权重' })}
                    <Input
                      aria-label={t(`${ns}.tier.weightFor`, {
                        defaultValue: '{{name}} 的权重',
                        name: entry.name,
                      })}
                      className='mdl-src__weight-input'
                      inputMode='numeric'
                      value={weights[entry.channel_id] ?? String(entry.weight)}
                      onChange={(event) =>
                        onWeightChange(entry.channel_id, event.target.value)
                      }
                    />
                  </span>
                ) : editing && entry.role === 'backup' ? (
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
                ) : (
                  <span className='mdl-cap mdl-src__meta'>
                    {[
                      roleName(entry),
                      t(`${ns}.tier.weightValue`, {
                        defaultValue: '权重 {{value}}',
                        value: weights[entry.channel_id] ?? entry.weight,
                      }),
                      formatLatency(entry.health.avg_latency_ms).replace(
                        /(\d)(m?s)$/,
                        '$1 $2',
                      ),
                    ].join(' · ')}
                  </span>
                )}
              </div>
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

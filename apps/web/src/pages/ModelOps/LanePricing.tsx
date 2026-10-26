import { useState } from 'react';
import {
  formatPerMtok,
  grossMargin,
  marginClass,
  poolCostBasis,
} from './formatters';
import LanePool from './LanePool';
import {
  derivedPrice,
  effectiveRatio,
  impliedRatio,
  PIN_KEYS,
  type LaneDraft,
  type OfficialPrice,
  type PinKey,
} from './ratioPricing';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Select } from '../../components/ui/select';
import { Textarea } from '../../components/ui/textarea';
import type { BillingMode, SellPriceRow } from './sellPrices';
import type { ModelOpsPoolEntry } from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps';

type LanePricingProps = {
  canEditPrice: boolean;
  currentRow: (lane: string) => SellPriceRow | undefined;
  drafts: Record<string, LaneDraft>;
  laneName: (lane: string) => string;
  laneRatio: (lane: string) => number;
  lanes: string[];
  official: OfficialPrice | null;
  onGoLive: (lane: string, entry: ModelOpsPoolEntry) => void;
  onPatch: (lane: string, patch: Partial<LaneDraft>) => void;
  onPromote: (lane: string, entry: ModelOpsPoolEntry) => void;
  pools: Record<string, ModelOpsPoolEntry[]>;
  t: TFunction;
  windowHours: number;
};

const pinLabel = (key: PinKey, t: TFunction) =>
  ({
    cache_read: t('pricing.cacheRead', { defaultValue: '缓存读取' }),
    cache_write_5m: t(`${ns}.price.cacheWrite`, { defaultValue: '缓存写入' }),
    input: t('pricing.input', { defaultValue: '输入' }),
    output: t('pricing.output', { defaultValue: '输出' }),
  })[key];

const percent = (value: number | null) =>
  value == null ? '—' : `${Math.round(value * 10) / 10}%`;

/** 定价与来源: one row per lane, priced as a percent of the official price.
 *  A row opens two things on demand — its single-price overrides and its
 *  sources — so the common case is one number per lane. */
export default function LanePricing({
  canEditPrice,
  currentRow,
  drafts,
  laneName,
  laneRatio,
  lanes,
  official,
  onGoLive,
  onPatch,
  onPromote,
  pools,
  t,
  windowHours,
}: LanePricingProps) {
  const [editing, setEditing] = useState<Set<string>>(new Set());
  const [poolOpen, setPoolOpen] = useState<Set<string>>(new Set());
  const toggle = (
    set: Set<string>,
    update: (next: Set<string>) => void,
    lane: string,
  ) => {
    const next = new Set(set);
    if (next.has(lane)) next.delete(lane);
    else next.add(lane);
    update(next);
  };

  return (
    <div className='mo-pricing'>
      <div className='mo-official'>
        <span className='mo-official__label'>
          {t(`${ns}.price.official`, { defaultValue: '官方价 $/M' })}
        </span>
        {official ? (
          (
            [
              ['input', official.input],
              ['output', official.output],
              ['cache_read', official.cacheRead],
              ['cache_write_5m', official.cacheWrite5m],
            ] as Array<[PinKey, number | null]>
          ).map(([key, value]) => (
            <span className='mo-official__cell' key={key}>
              <span className='mo-official__name'>{pinLabel(key, t)}</span>
              <span className='mo-official__value'>
                {value == null ? '—' : formatPerMtok(value)}
              </span>
            </span>
          ))
        ) : (
          <span className='mo-sub'>
            {t(`${ns}.price.noOfficial`, {
              defaultValue: '没有官方价，按固定价定价。',
            })}
          </span>
        )}
      </div>

      <div className='mo-ltable' role='table'>
        <div className='mo-ltable__row mo-ltable__row--head' role='row'>
          <span role='columnheader'>
            {t(`${ns}.price.colLane`, { defaultValue: '线路' })}
          </span>
          <span role='columnheader'>
            {t(`${ns}.price.colRatio`, { defaultValue: '官方价的' })}
          </span>
          <span role='columnheader'>
            {t(`${ns}.price.colSell`, { defaultValue: '售价 $/M 入 / 出' })}
          </span>
          <span role='columnheader'>
            {t(`${ns}.price.colCost`, { defaultValue: '成本' })}
          </span>
          <span role='columnheader'>
            {t(`${ns}.price.colMargin`, { defaultValue: '毛利' })}
          </span>
          <span role='columnheader' />
        </div>
        {lanes.map((lane) => {
          const draft = drafts[lane];
          if (!draft) return null;
          const pool = pools[lane] ?? [];
          const name = laneName(lane);
          if (!draft.exists) {
            return (
              <div className='mo-ltable__row' key={lane} role='row'>
                <span className='mo-ltable__name' role='cell'>
                  {name}
                </span>
                <span className='mo-sub' role='cell'>
                  {t(`${ns}.price.laneOff`, { defaultValue: '未开' })}
                </span>
                <span role='cell' />
                <span role='cell' />
                <span role='cell' />
                <span className='mo-ltable__actions' role='cell'>
                  <Button
                    disabled={!canEditPrice}
                    size='xs'
                    variant='secondary'
                    onClick={() => onPatch(lane, { exists: true })}
                  >
                    {t(`${ns}.price.openLane`, { defaultValue: '开这条线路' })}
                  </Button>
                </span>
              </div>
            );
          }
          const current = currentRow(lane);
          const price = derivedPrice(draft, official, laneRatio(lane), current);
          const following = draft.mode === 'follow' && Boolean(official);
          const pinCount = following ? Object.keys(draft.pins).length : 0;
          const perToken = draft.billingMode === 'per_token' || following;
          const cost = poolCostBasis(pool);
          const costShare =
            official?.input && cost.input != null
              ? (cost.input / official.input) * 100
              : null;
          const margin = perToken
            ? grossMargin(price.input, price.output, cost.input, cost.output)
            : null;
          const ratioShown = following
            ? effectiveRatio(draft, laneRatio(lane))
            : impliedRatio(price.input, official);
          const isEditing = editing.has(lane);
          const isPoolOpen = poolOpen.has(lane);
          const sellText = perToken
            ? `${formatPerMtok(price.input)} / ${formatPerMtok(price.output)}`
            : draft.billingMode === 'per_call'
              ? `$${draft.perCall || '—'}${t(`${ns}.tier.perCallUnit`, { defaultValue: '/ 次' })}`
              : `$${draft.perSecond || '—'}${t(`${ns}.tier.perSecondUnit`, { defaultValue: '/ 秒' })}`;

          return (
            <div className='mo-ltable__group' key={lane} role='rowgroup'>
              <div className='mo-ltable__row' role='row'>
                <span className='mo-ltable__name' role='cell'>
                  {name}
                </span>
                <span className='mo-ltable__ratio' role='cell'>
                  {following ? (
                    <>
                      <Input
                        aria-label={t(`${ns}.price.ratioFor`, {
                          defaultValue: '{{lane}}售价比例',
                          lane: name,
                        })}
                        className='mo-ltable__ratio-input'
                        disabled={!canEditPrice}
                        inputMode='decimal'
                        placeholder={String(laneRatio(lane))}
                        value={draft.ratio}
                        onChange={(event) =>
                          onPatch(lane, { ratio: event.target.value })
                        }
                      />
                      <span className='mo-ltable__unit'>%</span>
                      <span className='mo-sub'>
                        {draft.ratio.trim() === ''
                          ? t(`${ns}.price.laneDefault`, {
                              defaultValue: '线路默认',
                            })
                          : t(`${ns}.price.ownRatio`, {
                              defaultValue: '单独',
                            })}
                      </span>
                    </>
                  ) : (
                    <>
                      <span className='mo-sub'>
                        {t(`${ns}.price.fixed`, { defaultValue: '固定价' })}
                        {ratioShown != null ? ` · ≈${percent(ratioShown)}` : ''}
                      </span>
                      {official &&
                      canEditPrice &&
                      draft.billingMode === 'per_token' ? (
                        <Button
                          size='xs'
                          variant='link'
                          onClick={() =>
                            onPatch(lane, {
                              mode: 'follow',
                              pins: {},
                              ratio: '',
                            })
                          }
                        >
                          {t(`${ns}.price.follow`, {
                            defaultValue: '改为跟随',
                          })}
                        </Button>
                      ) : null}
                    </>
                  )}
                </span>
                <span className='mo-ltable__sell' role='cell'>
                  <span className='mo-num'>{sellText}</span>
                  {pinCount > 0 ? (
                    <Badge variant='warning'>
                      {t(`${ns}.price.pinnedCount`, {
                        count: pinCount,
                        defaultValue: '{{count}} 项固定',
                      })}
                    </Badge>
                  ) : null}
                  <Button
                    aria-expanded={isEditing}
                    size='xs'
                    variant='ghost'
                    onClick={() => toggle(editing, setEditing, lane)}
                  >
                    {following
                      ? t(`${ns}.price.editItems`, { defaultValue: '改单项' })
                      : t(`${ns}.price.editPrice`, { defaultValue: '改价格' })}
                  </Button>
                </span>
                <span className='mo-num' role='cell'>
                  {costShare != null
                    ? percent(costShare)
                    : cost.input != null
                      ? `$${formatPerMtok(cost.input)}`
                      : '—'}
                </span>
                <span
                  className={`mo-num ${margin != null ? marginClass(margin) : ''}`}
                  role='cell'
                >
                  {margin == null ? '—' : percent(margin * 100)}
                </span>
                <span className='mo-ltable__actions' role='cell'>
                  <Button
                    aria-expanded={isPoolOpen}
                    size='xs'
                    variant='ghost'
                    onClick={() => toggle(poolOpen, setPoolOpen, lane)}
                  >
                    {t(`${ns}.price.sources`, {
                      count: pool.length,
                      defaultValue: '{{count}} 个来源',
                    })}
                  </Button>
                </span>
              </div>

              {isEditing && following && official ? (
                <div className='mo-ltable__panel'>
                  <div className='mo-ltable__panel-head'>
                    <span className='mo-sect'>
                      {t(`${ns}.price.itemsTitle`, {
                        defaultValue: '单项改价',
                      })}
                    </span>
                    <span className='mo-sub'>
                      {t(`${ns}.price.itemsHint`, {
                        defaultValue: '留空 = 跟随比例',
                      })}
                    </span>
                  </div>
                  <div className='mo-ltable__fields'>
                    {PIN_KEYS.map((key) => {
                      const pinned = draft.pins[key] !== undefined;
                      const followed = derivedPrice(
                        { ...draft, pins: {} },
                        official,
                        laneRatio(lane),
                        current,
                      )[key];
                      return (
                        <label className='mo-field' key={key}>
                          <span className='mo-field__label'>
                            {pinLabel(key, t)} $/M
                          </span>
                          <Input
                            className={pinned ? 'mo-field__pinned' : undefined}
                            disabled={!canEditPrice}
                            inputMode='decimal'
                            placeholder={
                              followed == null ? '—' : formatPerMtok(followed)
                            }
                            value={draft.pins[key] ?? ''}
                            onChange={(event) => {
                              const next = { ...draft.pins };
                              if (event.target.value === '') delete next[key];
                              else next[key] = event.target.value;
                              onPatch(lane, { pins: next });
                            }}
                          />
                          <span className='mo-sub'>
                            {pinned ? (
                              <>
                                {t(`${ns}.price.pinnedWas`, {
                                  defaultValue: '固定 · 原 {{value}}',
                                  value: formatPerMtok(followed),
                                })}{' '}
                                <Button
                                  size='xs'
                                  variant='link'
                                  onClick={() => {
                                    const next = { ...draft.pins };
                                    delete next[key];
                                    onPatch(lane, { pins: next });
                                  }}
                                >
                                  {t(`${ns}.price.restore`, {
                                    defaultValue: '恢复',
                                  })}
                                </Button>
                              </>
                            ) : (
                              t(`${ns}.price.following`, {
                                defaultValue: '跟随',
                              })
                            )}
                          </span>
                        </label>
                      );
                    })}
                  </div>
                  <div>
                    <Button
                      disabled={!canEditPrice}
                      size='xs'
                      variant='link'
                      onClick={() =>
                        onPatch(lane, {
                          billingMode: 'per_token',
                          cacheRead: String(price.cache_read ?? ''),
                          cacheWrite5m: String(price.cache_write_5m ?? ''),
                          input: String(price.input ?? ''),
                          mode: 'fixed',
                          output: String(price.output ?? ''),
                          pins: {},
                        })
                      }
                    >
                      {t(`${ns}.price.toFixed`, {
                        defaultValue: '改为固定价',
                      })}
                    </Button>
                  </div>
                </div>
              ) : null}

              {isEditing && !following ? (
                <div className='mo-ltable__panel'>
                  <div className='mo-ltable__panel-head'>
                    <span className='mo-sect'>
                      {t(`${ns}.price.fixedTitle`, { defaultValue: '固定价' })}
                    </span>
                    <Select
                      aria-label={t(`${ns}.tier.billingMode`, {
                        defaultValue: '计价方式',
                      })}
                      className='mo-ltable__mode'
                      disabled={!canEditPrice || Boolean(draft.imagePricing)}
                      options={[
                        {
                          label: t(`${ns}.price.perToken`, {
                            defaultValue: '按 token',
                          }),
                          value: 'per_token',
                        },
                        {
                          label: t(`${ns}.price.perCall`, {
                            defaultValue: '按次',
                          }),
                          value: 'per_call',
                        },
                        {
                          label: t(`${ns}.price.perSecond`, {
                            defaultValue: '按秒',
                          }),
                          value: 'per_second',
                        },
                      ]}
                      size='sm'
                      value={draft.billingMode}
                      onValueChange={(value) =>
                        onPatch(lane, { billingMode: value as BillingMode })
                      }
                    />
                  </div>
                  <div className='mo-ltable__fields'>
                    {(draft.billingMode === 'per_token'
                      ? ([
                          ['input', `${pinLabel('input', t)} $/M`],
                          ['output', `${pinLabel('output', t)} $/M`],
                          ['cacheRead', `${pinLabel('cache_read', t)} $/M`],
                          [
                            'cacheWrite5m',
                            t('pricing.cacheWrite5m', {
                              defaultValue: '缓存写入 · 5 分钟',
                            }) + ' $/M',
                          ],
                          [
                            'cacheWrite1h',
                            t('pricing.cacheWrite1h', {
                              defaultValue: '缓存写入 · 1 小时',
                            }) + ' $/M',
                          ],
                        ] as const)
                      : draft.billingMode === 'per_call'
                        ? ([
                            [
                              'perCall',
                              t(`${ns}.price.perCallPrice`, {
                                defaultValue: '每次 $',
                              }),
                            ],
                          ] as const)
                        : ([
                            [
                              'perSecond',
                              t(`${ns}.price.perSecondPrice`, {
                                defaultValue: '每秒 $',
                              }),
                            ],
                          ] as const)
                    ).map(([field, label]) => (
                      <label className='mo-field' key={field}>
                        <span className='mo-field__label'>{label}</span>
                        <Input
                          disabled={
                            !canEditPrice ||
                            (field === 'perCall' && Boolean(draft.imagePricing))
                          }
                          inputMode='decimal'
                          value={
                            field === 'perCall' && draft.imagePricing
                              ? draft.imagePricing.output_usd
                              : draft[field]
                          }
                          onChange={(event) =>
                            onPatch(lane, { [field]: event.target.value })
                          }
                        />
                      </label>
                    ))}
                    {draft.imagePricing
                      ? Object.entries(draft.imagePricing).map(
                          ([key, amount]) => (
                            <label className='mo-field' key={key}>
                              <span className='mo-field__label'>
                                {key}
                                {key.endsWith('_usd') ? ' ($)' : ''}
                              </span>
                              <Input
                                disabled={!canEditPrice}
                                inputMode='decimal'
                                value={amount}
                                onChange={(event) =>
                                  onPatch(lane, {
                                    imagePricing: {
                                      ...draft.imagePricing!,
                                      [key]: Number(event.target.value),
                                    },
                                  })
                                }
                              />
                            </label>
                          ),
                        )
                      : null}
                    {draft.billingMode === 'per_second' ? (
                      <label className='mo-field mo-field--wide'>
                        <span className='mo-field__label'>
                          {t(`${ns}.tier.videoPricing`, {
                            defaultValue: '视频价格 JSON',
                          })}
                        </span>
                        <Textarea
                          disabled={!canEditPrice}
                          placeholder='{"resolution_second_usd":{"720p":0.05,"1080p":0.1}}'
                          rows={3}
                          value={draft.videoPricing}
                          onChange={(event) =>
                            onPatch(lane, { videoPricing: event.target.value })
                          }
                        />
                        <span className='mo-field__hint'>
                          {t(`${ns}.tier.videoPricingHelp`, {
                            defaultValue:
                              'resolution_second_usd 按档秒价；provider_cost_multiplier 以上游 cost 为准（provider_cost_multiplier_by_resolution 可按分辨率分别设置），仍需填秒价用于预扣；按 token 计费填输出单价即可。',
                          })}
                        </span>
                      </label>
                    ) : null}
                    <label className='mo-field'>
                      <span className='mo-field__label'>
                        {t(`${ns}.tier.peakHours`, {
                          defaultValue: '高峰 UTC',
                        })}
                      </span>
                      <Input
                        disabled={!canEditPrice}
                        placeholder='1-4,6-10'
                        value={draft.peakHours}
                        onChange={(event) =>
                          onPatch(lane, { peakHours: event.target.value })
                        }
                      />
                    </label>
                    <label className='mo-field'>
                      <span className='mo-field__label'>
                        {t(`${ns}.tier.peakMultiplier`, {
                          defaultValue: '倍率 ×',
                        })}
                      </span>
                      <Input
                        disabled={!canEditPrice}
                        inputMode='decimal'
                        placeholder='2'
                        value={draft.peakMultiplier}
                        onChange={(event) =>
                          onPatch(lane, { peakMultiplier: event.target.value })
                        }
                      />
                    </label>
                  </div>
                  {official && draft.billingMode === 'per_token' ? (
                    <div>
                      <Button
                        disabled={!canEditPrice}
                        size='xs'
                        variant='link'
                        onClick={() =>
                          onPatch(lane, { mode: 'follow', pins: {}, ratio: '' })
                        }
                      >
                        {t(`${ns}.price.toFollow`, {
                          defaultValue: '改为跟随官方价',
                        })}
                      </Button>
                    </div>
                  ) : null}
                </div>
              ) : null}

              {isPoolOpen ? (
                <div className='mo-ltable__panel mo-ltable__panel--pool'>
                  <LanePool
                    pool={pool}
                    t={t}
                    weights={draft.weights}
                    windowHours={windowHours}
                    onGoLive={(entry) => onGoLive(lane, entry)}
                    onPromote={(entry) => onPromote(lane, entry)}
                    onWeightChange={(channelId, value) =>
                      onPatch(lane, {
                        weights: { ...draft.weights, [channelId]: value },
                      })
                    }
                  />
                </div>
              ) : null}
            </div>
          );
        })}
      </div>
      <p className='mo-sub'>
        {official
          ? t(`${ns}.price.followNote`, {
              defaultValue: '缓存、长上下文同比例。官方价变动时售价自动跟随。',
            })
          : null}
      </p>
    </div>
  );
}

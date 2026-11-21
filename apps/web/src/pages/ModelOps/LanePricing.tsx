import { Plus } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router-dom';
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../../components/ui/dropdown-menu';
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
  modelName: string;
  official: OfficialPrice | null;
  onGoLive: (lane: string, entry: ModelOpsPoolEntry) => void;
  onPatch: (lane: string, patch: Partial<LaneDraft>) => void;
  onPromote: (lane: string, entry: ModelOpsPoolEntry) => void;
  /** Puts a lane back the way it was saved. */
  onReset: (lane: string) => void;
  onSave: () => void;
  pools: Record<string, ModelOpsPoolEntry[]>;
  saving: boolean;
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

/* Cents from a dime up ($0.90, $18.75); below that the digits that matter. */
const dollars = (value: number | null | undefined) =>
  value == null || !Number.isFinite(value)
    ? '—'
    : `$${value >= 0.1 ? value.toFixed(2) : formatPerMtok(value)}`;

const numberOr = (value: string) => {
  const number = Number(value);
  return value.trim() !== '' && Number.isFinite(number) ? number : null;
};

/** 线路: one card per open lane, side by side. A card reads its sell price
 *  in large figures with the margin, then its sources; 改价 opens the price
 *  and the source weights in place. */
export default function LanePricing({
  canEditPrice,
  currentRow,
  drafts,
  laneName,
  laneRatio,
  lanes,
  modelName,
  official,
  onGoLive,
  onPatch,
  onPromote,
  onReset,
  onSave,
  pools,
  saving,
  t,
  windowHours,
}: LanePricingProps) {
  const [editing, setEditing] = useState<Set<string>>(new Set());
  const setLaneEditing = (lane: string, on: boolean) =>
    setEditing((current) => {
      const next = new Set(current);
      if (on) next.add(lane);
      else next.delete(lane);
      return next;
    });

  const closed = lanes.filter((lane) => drafts[lane] && !drafts[lane].exists);
  const openLane = (lane: string) => {
    onPatch(lane, { exists: true });
    setLaneEditing(lane, true);
  };
  const newLaneLabel = t(`${ns}.price.newLane`, { defaultValue: '新线路' });

  return (
    <section className='mdl-lanes'>
      <div className='mdl-lanes__head'>
        <h3 className='mdl-sec'>
          {t(`${ns}.price.colLane`, { defaultValue: '线路' })}
        </h3>
        <span className='mdl-lanes__tools'>
          <span className='mdl-cap'>
            {t(`${ns}.price.unit`, { defaultValue: '$ / 百万 token' })}
          </span>
          {canEditPrice && closed.length === 1 ? (
            <Button
              size='sm'
              variant='secondary'
              onClick={() => openLane(closed[0])}
            >
              <Plus aria-hidden='true' size={14} />
              {newLaneLabel}
            </Button>
          ) : canEditPrice && closed.length > 1 ? (
            <DropdownMenu>
              <DropdownMenuTrigger>
                <Button size='sm' variant='secondary'>
                  <Plus aria-hidden='true' size={14} />
                  {newLaneLabel}
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align='end'>
                {closed.map((lane) => (
                  <DropdownMenuItem key={lane} onSelect={() => openLane(lane)}>
                    {laneName(lane)}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          ) : null}
        </span>
      </div>
      <div className='mdl-lanes__grid'>
        {lanes.map((lane) => {
          const draft = drafts[lane];
          if (!draft || !draft.exists) return null;
          const pool = pools[lane] ?? [];
          const name = laneName(lane);
          const current = currentRow(lane);
          const price = derivedPrice(draft, official, laneRatio(lane), current);
          const following = draft.mode === 'follow' && Boolean(official);
          const pinCount = following ? Object.keys(draft.pins).length : 0;
          const perToken = draft.billingMode === 'per_token' || following;
          const cost = poolCostBasis(pool);
          const margin = perToken
            ? grossMargin(price.input, price.output, cost.input, cost.output)
            : null;
          const ratioShown = following
            ? effectiveRatio(draft, laneRatio(lane))
            : impliedRatio(price.input, official);
          const isEditing = editing.has(lane);
          const cache1h = following
            ? (current?.cache_write_1h_per_mtok ?? null)
            : numberOr(draft.cacheWrite1h);
          const caption = [
            price.cache_read != null
              ? t(`${ns}.price.capCacheRead`, {
                  defaultValue: '缓存读 {{value}}',
                  value: dollars(price.cache_read),
                })
              : '',
            price.cache_write_5m != null
              ? t(`${ns}.price.capCacheWrite`, {
                  defaultValue: '写 5m {{value}}',
                  value: dollars(price.cache_write_5m),
                })
              : '',
            cache1h != null
              ? t(`${ns}.price.capCacheWrite1h`, {
                  defaultValue: '1h {{value}}',
                  value: dollars(cache1h),
                })
              : '',
            cost.input != null || cost.output != null
              ? t(`${ns}.price.capCost`, {
                  defaultValue: '成本 {{input}} / {{output}}',
                  input: dollars(cost.input),
                  output: dollars(cost.output),
                })
              : cost.request != null
                ? t(`${ns}.price.capCostCall`, {
                    defaultValue: '成本 {{value}} / 次',
                    value: dollars(cost.request),
                  })
                : '',
            ratioShown != null ? `× ${(ratioShown / 100).toFixed(2)}` : '',
          ].filter(Boolean);
          const figures: Array<[string, string, string?]> = perToken
            ? [
                [
                  'input',
                  t('pricing.input', { defaultValue: '输入' }),
                  dollars(price.input),
                ],
                [
                  'output',
                  t('pricing.output', { defaultValue: '输出' }),
                  dollars(price.output),
                ],
              ]
            : [
                [
                  'unit',
                  draft.billingMode === 'per_call'
                    ? t(`${ns}.price.perCallPrice`, { defaultValue: '每次 $' })
                    : t(`${ns}.price.perSecondPrice`, {
                        defaultValue: '每秒 $',
                      }),
                  `$${
                    (draft.billingMode === 'per_call'
                      ? (draft.imagePricing?.output_usd ?? draft.perCall)
                      : draft.perSecond) || '—'
                  }`,
                ],
              ];

          return (
            <section
              aria-label={name}
              className={isEditing ? 'mdl-lane is-editing' : 'mdl-lane'}
              key={lane}
            >
              <div className='mdl-lane__head'>
                <div className='mdl-lane__title'>
                  <h4 className='mdl-lane__name'>{name}</h4>
                  <Badge variant='neutral'>
                    {following
                      ? pinCount > 0
                        ? t(`${ns}.price.followPinned`, {
                            count: pinCount,
                            defaultValue: '跟随官方价 · {{count}} 项固定',
                          })
                        : t(`${ns}.price.followBadge`, {
                            defaultValue: '跟随官方价',
                          })
                      : t(`${ns}.price.fixed`, { defaultValue: '固定价' })}
                  </Badge>
                </div>
                {!isEditing ? (
                  <Button
                    size='sm'
                    variant='secondary'
                    onClick={() => setLaneEditing(lane, true)}
                  >
                    {t(`${ns}.price.edit`, { defaultValue: '改价' })}
                  </Button>
                ) : null}
              </div>
              <div className='mdl-lane__figures'>
                {figures.map(([key, label, value]) => (
                  <div className='mdl-figure' key={key}>
                    <p className='mdl-cap'>{label}</p>
                    <p className='mdl-big'>{value}</p>
                  </div>
                ))}
                <div className='mdl-figure'>
                  <p className='mdl-cap'>
                    {t(`${ns}.price.colMargin`, { defaultValue: '毛利' })}
                  </p>
                  <p
                    className={`mdl-big ${margin != null ? marginClass(margin) : ''}`}
                  >
                    {margin == null ? '—' : percent(margin * 100)}
                  </p>
                </div>
              </div>
              {caption.length ? (
                <p className='mdl-cap mdl-lane__caption'>
                  {caption.join(' · ')}
                </p>
              ) : null}

              {isEditing && following ? (
                <div className='mo-ltable__panel'>
                  <div className='mo-ltable__panel-head'>
                    <span className='mo-sect'>
                      {t(`${ns}.price.colRatio`, { defaultValue: '官方价的' })}
                    </span>
                    <span className='mo-ltable__ratio'>
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
                    </span>
                  </div>
                </div>
              ) : null}
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

              <LanePool
                editing={isEditing}
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
              <div className='mdl-lane__foot'>
                <Link
                  className='mdl-link'
                  to={`/console/model-ops/sources?model=${encodeURIComponent(modelName)}`}
                >
                  {t(`${ns}.price.addSource`, { defaultValue: '+ 来源' })}
                </Link>
                {isEditing ? (
                  <span className='mdl-lane__actions'>
                    <Button
                      disabled={saving}
                      size='sm'
                      variant='secondary'
                      onClick={() => {
                        onReset(lane);
                        setLaneEditing(lane, false);
                      }}
                    >
                      {t('common.cancel', { defaultValue: '取消' })}
                    </Button>
                    <Button
                      loading={saving}
                      size='sm'
                      variant='primary'
                      onClick={onSave}
                    >
                      {t('common.save', { defaultValue: '保存' })}
                    </Button>
                  </span>
                ) : null}
              </div>
            </section>
          );
        })}
      </div>
    </section>
  );
}

import { memo, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import {
  applyLanePeak,
  discountMarkOf,
  formatTierThreshold,
  heroPriceOf,
  laneContextTiers,
  personalPricing,
  applyPersonalPeak,
  laneOfficialRatio,
  listPriceOf,
  officialHeroPrice,
  planZheOf,
  peakActiveNow,
  type CatalogModel,
  type CatalogPricing,
  type ContextTier,
  type Lane,
} from './catalog';
import {
  StatusBars,
  StatusBarsLegend,
  StatusDot,
  formatSliceRate,
  formatSliceWhen,
  formatUptime,
  type StatusBarSlice,
  type StatusBarTone,
} from '../../components/ui';
import { ImagePriceDetails } from '../../components/ui/image-price-details';

/* One lane of a model, drawn the way the health page draws a lane: its name,
   discount and availability on top, the price on its own line, the week's
   thin bars, and the lane's speed underneath. The card and the detail modal
   both use it; the modal adds the cache prices. */

/** The week the catalog publishes: 84 two-hour slices. */
const WEEK_SLICES = 84;
const WEEK_SLICE_SECONDS = 7200;

export const money = (value?: number) =>
  value == null ? null : `$${value < 1 ? value.toFixed(3) : value.toFixed(2)}`;

const formatMs = (value?: number) => {
  if (value == null || value <= 0) return undefined;
  return value >= 1000
    ? `${(value / 1000).toFixed(2)}s`
    : `${Math.round(value)}ms`;
};

const pricePick = (
  p: CatalogPricing | undefined,
  key: 'cache_read' | 'cache_write_5m' | 'cache_write_1h',
) => p?.[`${key}_per_million`] ?? p?.[key];

type LaneStatus = 'operational' | 'degraded' | 'outage' | 'unknown';

const laneStatusOf = (health?: Lane['health']): LaneStatus => {
  const status = String(health?.status || '').toLowerCase();
  if (['unavailable', 'down', 'cooldown', 'circuit_open'].includes(status))
    return 'outage';
  if (status === 'degraded' || status === 'warning') return 'degraded';
  if (['available', 'ok', 'healthy'].includes(status)) return 'operational';
  return 'unknown';
};

export function useLaneLabel() {
  const { t } = useTranslation();
  return (lane: Lane) =>
    lane.key === 'stable'
      ? t('page.console.plaza.lane.stable', { defaultValue: '稳定' })
      : t('page.console.plaza.lane.eco', { defaultValue: 'Agent' });
}

/** The colour key above the plaza grid and the modal's lanes. */
export function LaneHealthLegend({ className }: { className?: string }) {
  const { t } = useTranslation();
  const tones: { tone: StatusBarTone; label: string }[] = [
    {
      tone: 'ok',
      label: t('page.console.channelHealth.boardLegend.ok', {
        defaultValue: '正常',
      }),
    },
    {
      tone: 'warn',
      label: t('page.console.channelHealth.boardLegend.warn', {
        defaultValue: '部分失败',
      }),
    },
    {
      tone: 'bad',
      label: t('page.console.channelHealth.boardLegend.bad', {
        defaultValue: '中断',
      }),
    },
    {
      tone: 'none',
      label: t('page.console.channelHealth.boardLegend.none', {
        defaultValue: '暂无数据',
      }),
    },
  ];
  return (
    <StatusBarsLegend
      caption={t('page.console.plaza.health.legendCaption', {
        defaultValue: '近 7 天 · 每格 2 小时',
      })}
      className={className}
      items={tones}
    />
  );
}

/* The plaza re-renders every card when a dialog opens or the search moves;
   a lane's week only redraws when its own record or label changes. The
   record comes straight from the catalog query, so its reference is stable. */
const LaneBars = memo(
  LaneBarsRecord,
  (prev, next) =>
    prev.label === next.label && prev.lane.health === next.lane.health,
);

function LaneBarsRecord({ lane, label }: { lane: Lane; label: string }) {
  const { t, i18n } = useTranslation();
  const health = lane.health;
  const language = i18n.language;
  const slices = useMemo<StatusBarSlice[]>(() => {
    const seconds = health?.bucket_seconds || WEEK_SLICE_SECONDS;
    const count = health?.buckets?.length || WEEK_SLICES;
    const byIndex = new Map((health?.buckets || []).map((b) => [b.index, b]));
    const toneWord = (tone: StatusBarTone) =>
      t(`page.console.channelHealth.tip.${tone}`, {
        defaultValue: {
          ok: '正常',
          warn: '部分失败',
          bad: '中断',
          none: '暂无数据',
          future: '还没到',
          blocked: '无法探测',
        }[tone],
      });
    return Array.from({ length: count }, (_, index) => {
      const bucket = byIndex.get(index);
      const tone: StatusBarTone =
        bucket && bucket.source !== 'none' ? bucket.tone : 'none';
      const rate = tone === 'none' ? undefined : bucket?.success_rate;
      return {
        tone,
        when: formatSliceWhen(bucket?.start_at, seconds, language),
        verdict:
          rate != null
            ? `${toneWord(tone)} · ${t('page.console.channelHealth.tip.rate', {
                defaultValue: '可用 {{value}}',
                value: formatSliceRate(rate),
              })}`
            : toneWord(tone),
      };
    });
  }, [health, language, t]);
  return (
    <StatusBars
      className='plaza-laneblock__bars'
      label={t('page.console.plaza.health.barsLabel', {
        defaultValue: '{{lane}}线路过去 7 天的记录',
        lane: label,
      })}
      slices={slices}
    />
  );
}

const laneHasRecord = (lane: Lane) =>
  (lane.health?.buckets || []).some((bucket) => bucket.source !== 'none');

export function LaneBlock({
  lane,
  model,
  quietStatus = false,
  variant,
}: {
  lane: Lane;
  model: CatalogModel;
  /** Draw the status dot without its word, when something above the lane
   *  already says it. */
  quietStatus?: boolean;
  /** The card is compact; the detail modal adds the cache line and a frame. */
  variant: 'card' | 'detail';
}) {
  const { t } = useTranslation();
  const laneLabel = useLaneLabel()(lane);

  if (lane.available === false) {
    return (
      <div
        className={`plaza-laneblock plaza-laneblock--${variant} plaza-laneblock--off`}
      >
        <div className='plaza-laneblock__head'>
          <span className='plaza-laneblock__label'>{laneLabel}</span>
          <span className='plaza-laneblock__off'>
            {t('page.console.plaza.lane.off', { defaultValue: '未开通' })}
          </span>
        </div>
      </div>
    );
  }

  const hero = heroPriceOf(lane, model);
  const mark = discountMarkOf(laneOfficialRatio(lane, model));
  const peakNow =
    peakActiveNow(lane.peak_hours_utc) && (lane.peak_multiplier ?? 0) > 0;
  const status = laneStatusOf(lane.health);
  const tiers = laneContextTiers(lane);
  const baseImages =
    lane.pricing?.image_pricing ?? model.pricing?.image_pricing;
  const personalImages = personalPricing(lane)?.image_pricing;
  const rawImages = personalImages ?? baseImages;
  const imagePrices = rawImages
    ? (Object.fromEntries(
        Object.entries(rawImages).map(([key, value]) => [
          key,
          key.endsWith('_usd')
            ? personalImages
              ? applyPersonalPeak(value, lane)
              : applyLanePeak(value, lane)
            : value,
        ]),
      ) as typeof rawImages)
    : undefined;

  const discounted = hero?.mine != null && hero.mine !== hero.list;
  const planZhe = discounted ? planZheOf(lane) : undefined;
  const official = hero ? officialHeroPrice(model, hero.kind) : undefined;
  const unit = !hero
    ? ''
    : hero.kind === 'perCall'
      ? imagePrices
        ? t('pricing.images.perImage') +
          (imagePrices.high_resolution_output_usd != null
            ? ` · ${t('pricing.images.from')}`
            : '')
        : t('page.console.plaza.price.perCallUnit', { defaultValue: '/ 次' })
      : hero.kind === 'perSecond'
        ? t('page.console.plaza.price.perSecondUnit', { defaultValue: '/ 秒' })
        : hero.kind === 'output'
          ? t('page.console.plaza.price.heroOutputUnit', {
              defaultValue: '/1M 出',
            })
          : t('page.console.plaza.price.heroInputUnit', {
              defaultValue: '/1M 入',
            });

  const figures = [
    formatMs(lane.health?.latency_p95_ms)
      ? t('page.console.plaza.health.p95', {
          defaultValue: 'p95 {{value}}',
          value: formatMs(lane.health?.latency_p95_ms),
        })
      : null,
    lane.health?.output_tps && lane.health.output_tps > 0
      ? t('page.console.plaza.health.tps', {
          defaultValue: '{{value}} tok/s',
          value: lane.health.output_tps.toFixed(0),
        })
      : null,
  ].filter(Boolean);

  const cache =
    variant === 'detail' ? cacheLine(lane, model, t) : ([] as string[]);

  return (
    <div className={`plaza-laneblock plaza-laneblock--${variant}`}>
      <div className='plaza-laneblock__head'>
        <span className='plaza-laneblock__label'>{laneLabel}</span>
        {mark ? (
          <span
            className={`plaza-mark plaza-mark--${mark.kind}`}
            title={
              mark.kind === 'off'
                ? t('page.console.plaza.discount.offHint', {
                    defaultValue: '相对官方价',
                  })
                : undefined
            }
          >
            {mark.kind === 'list'
              ? t('page.console.plaza.discount.list', {
                  defaultValue: '官方价',
                })
              : t('page.console.plaza.discount.off', {
                  defaultValue: '{{zhe}}折',
                  off: mark.off,
                  zhe: mark.zhe,
                })}
          </span>
        ) : null}
        {planZhe != null ? (
          <span className='plaza-mark plaza-mark--plan'>
            {t('page.console.plaza.discount.plan', {
              defaultValue: '专属 {{zhe}}折',
              off: lane.plan_discount_pct,
              zhe: planZhe,
            })}
          </span>
        ) : null}
        {peakNow ? (
          <span className='plaza-mark plaza-mark--peak'>
            {t('page.console.plaza.price.peakNow', {
              defaultValue: '峰时 ×{{times}}',
              times: lane.peak_multiplier,
            })}
          </span>
        ) : null}
        <span className='plaza-laneblock__spacer' />
        <StatusDot
          className={`plaza-laneblock__status plaza-laneblock__status--${status}`}
          tone={
            status === 'operational'
              ? 'success'
              : status === 'degraded'
                ? 'warning'
                : status === 'outage'
                  ? 'danger'
                  : 'neutral'
          }
        >
          {status === 'operational' ||
          status === 'unknown' ||
          quietStatus ? null : (
            <span>
              {status === 'outage'
                ? t('page.console.plaza.health.unavailable', {
                    defaultValue: '当前不可用',
                  })
                : t('page.console.channelHealth.statusWord.degraded', {
                    defaultValue: '降级',
                  })}
            </span>
          )}
        </StatusDot>
        <span
          className='plaza-laneblock__uptime'
          title={t('page.console.plaza.health.uptimeHint', {
            defaultValue: '过去 7 天可用',
          })}
        >
          {formatUptime(lane.health?.availability)}
        </span>
      </div>

      {hero ? (
        <div className='plaza-laneblock__price'>
          {discounted && planZhe == null ? (
            <s className='plaza-laneblock__was'>{money(hero.list)}</s>
          ) : null}
          <b className='plaza-laneblock__now'>
            {money(discounted ? hero.mine : hero.list)}
          </b>
          <span className='plaza-laneblock__unit'>{unit}</span>
          {hero.listOut != null ? (
            <span className='plaza-laneblock__out'>
              ·{' '}
              {discounted && hero.mineOut != null ? (
                planZhe != null ? (
                  money(hero.mineOut)
                ) : (
                  <>
                    <s>{money(hero.listOut)}</s> {money(hero.mineOut)}
                  </>
                )
              ) : (
                money(hero.listOut)
              )}{' '}
              {t('page.console.plaza.price.outputUnit', {
                defaultValue: '出',
              })}
            </span>
          ) : null}
        </div>
      ) : null}

      {hero && planZhe != null && variant === 'card' ? (
        <div className='plaza-laneblock__plan'>
          {official != null ? (
            <>
              {t('page.console.plaza.discount.officialWas', {
                defaultValue: '官方',
              })}{' '}
              <s>{money(official)}</s>
              {' · '}
            </>
          ) : null}
          {laneLabel}{' '}
          {mark?.kind === 'off'
            ? t('page.console.plaza.discount.off', {
                defaultValue: '{{zhe}}折',
                zhe: mark.zhe,
              })
            : t('page.console.plaza.discount.list', {
                defaultValue: '官方价',
              })}
          {' · '}
          <b>
            {t('page.console.plaza.discount.plan', {
              defaultValue: '专属 {{zhe}}折',
              off: lane.plan_discount_pct,
              zhe: planZhe,
            })}
          </b>
        </div>
      ) : null}

      {hero && planZhe != null && variant === 'detail' ? (
        <PlanPriceBars
          laneLabel={laneLabel}
          mine={hero.mine}
          official={official}
          standard={hero.list}
        />
      ) : null}

      {variant === 'detail' && imagePrices ? (
        <ImagePriceDetails price={imagePrices} />
      ) : null}

      {cache.length ? (
        <div className='plaza-laneblock__cache'>{cache.join(' · ')}</div>
      ) : null}

      {tiers.map((tier) => (
        <LaneTierLine key={tier.threshold} tier={tier} />
      ))}

      {/* A week with no record at all is said in words: a row of empty grey
          bars reads as "something is wrong", not "nothing has run yet". The
          lane's own figures still show when it has them. */}
      {laneHasRecord(lane) ? <LaneBars label={laneLabel} lane={lane} /> : null}
      {laneHasRecord(lane) || figures.length ? (
        /* A lane with no figures keeps the line, so lane blocks side by
           side stay the same height. */
        <span className='plaza-laneblock__figures'>
          {figures.length ? figures.join(' · ') : '—'}
        </span>
      ) : (
        <span className='plaza-laneblock__norecord'>
          {t('page.console.plaza.health.noRecord', {
            defaultValue: '暂无数据 · 有请求后开始记录',
          })}
        </span>
      )}
    </div>
  );
}

function cacheLine(
  lane: Lane,
  model: CatalogModel,
  t: ReturnType<typeof useTranslation>['t'],
) {
  const pick = (key: 'cache_read' | 'cache_write_5m' | 'cache_write_1h') => {
    const mine = pricePick(personalPricing(lane), key);
    if (mine != null) return applyPersonalPeak(mine, lane);
    return applyLanePeak(
      pricePick(lane.pricing, key) ??
        (lane.key === 'stable' ? pricePick(model.pricing, key) : undefined),
      lane,
    );
  };
  const read = pick('cache_read');
  const write5m = pick('cache_write_5m');
  const write1h = pick('cache_write_1h');
  const parts: string[] = [];
  if (read != null)
    parts.push(
      t('page.console.plaza.detail.cacheRead', {
        defaultValue: '缓存读 {{price}}',
        price: money(read),
      }),
    );
  if (write5m != null || write1h != null) {
    const writes = [
      write5m != null ? `5m ${money(write5m)}` : null,
      write1h != null ? `1h ${money(write1h)}` : null,
    ]
      .filter(Boolean)
      .join(' · ');
    parts.push(
      t('page.console.plaza.detail.cacheWrite', {
        defaultValue: '缓存写 {{prices}}',
        prices: writes,
      }),
    );
  }
  return parts;
}

/** The long-context line: the price shown above is what almost every request
 *  pays; this says where that stops and what it becomes. */
function LaneTierLine({ tier }: { tier: ContextTier }) {
  const { t } = useTranslation();
  const input = tier.input;
  const output = tier.output;
  const prices = [money(input), money(output)].filter(Boolean).join(' / ');
  if (!prices) return null;
  return (
    <span className='plaza-laneblock__tier'>
      {t('page.console.plaza.tier.cardLine', {
        defaultValue: '输入超过 {{threshold}}：{{prices}}',
        prices,
        threshold: formatTierThreshold(tier.threshold),
      })}
    </span>
  );
}

/** Official, the lane's standard and the viewer's 专属 price as three bars on
 *  one scale, so how much the 专属折扣 saves reads at a glance. */
function PlanPriceBars({
  laneLabel,
  mine,
  official,
  standard,
}: {
  laneLabel: string;
  mine?: number;
  official?: number;
  standard: number;
}) {
  const { t } = useTranslation();
  const rows = [
    {
      key: 'official',
      label: t('page.console.plaza.discount.officialWas', {
        defaultValue: '官方',
      }),
      value: official,
    },
    { key: 'standard', label: laneLabel, value: standard },
    {
      key: 'mine',
      label: t('page.console.plaza.discount.planShort', {
        defaultValue: '专属',
      }),
      value: mine,
    },
  ].filter((row): row is { key: string; label: string; value: number } =>
    Number.isFinite(row.value),
  );
  const scale = Math.max(...rows.map((row) => row.value));
  if (!(scale > 0)) return null;
  const base = official ?? standard;
  const saved =
    mine != null && base > 0 ? Math.round((1 - mine / base) * 100) : undefined;
  return (
    <div className='plaza-planbars'>
      {rows.map((row) => (
        <div className={`plaza-planbars__row is-${row.key}`} key={row.key}>
          <span className='plaza-planbars__label'>{row.label}</span>
          <span className='plaza-planbars__track'>
            <span
              className='plaza-planbars__fill'
              style={{ width: `${(row.value / scale) * 100}%` }}
            />
          </span>
          <span className='plaza-planbars__value'>{money(row.value)}</span>
        </div>
      ))}
      {saved != null && saved > 0 ? (
        <span className='plaza-planbars__saved'>
          {t(
            official != null
              ? 'page.console.plaza.discount.savedVsOfficial'
              : 'page.console.plaza.discount.savedVsStandard',
            {
              defaultValue:
                official != null ? '比官方省 {{pct}}%' : '比标准价省 {{pct}}%',
              pct: saved,
            },
          )}
        </span>
      ) : null}
    </div>
  );
}

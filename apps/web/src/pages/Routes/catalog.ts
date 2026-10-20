import type { ImagePricing } from '../../lib/image-pricing';
export type LaneKey = 'eco' | 'stable';
export type PeakCalendar = { years: number[]; holidays: string[] };

export type LaneHealthBucket = {
  index: number;
  start_at: string;
  /** 'sample' from current servers; 'traffic' and 'probe' only in payloads
   *  cached before the catalog stopped naming provenance. */
  source: 'sample' | 'traffic' | 'probe' | 'none';
  success_rate?: number;
  tone: 'ok' | 'warn' | 'bad' | 'none';
};

export type Lane = {
  available?: boolean;
  discount_percent?: number;
  health?: {
    availability?: number;
    bucket_seconds?: number;
    buckets?: LaneHealthBucket[];
    latency_p95_ms?: number;
    /** 'sample' when the window holds measured requests (customer traffic and
     *  the gateway's streamed probes, which the catalog does not tell apart),
     *  'none' when it holds nothing. */
    observed_window?: string;
    output_tps?: number;
    status?: string;
  };
  input_per_million?: number;
  key: LaneKey;
  output_per_million?: number;
  peak_calendar?: PeakCalendar | null;
  peak_hours_utc?: string | null;
  peak_multiplier?: number | null;
  per_call?: number;
  per_second?: number;
  pricing?: CatalogPricing;
  /** The viewer's 专属折扣 on this lane: percent off the official price
   *  (40 = 6折). Set only when their price plan prices the lane. */
  plan_discount_pct?: number;
  your_price?: CatalogPricing;
};

/** Long-context pricing: once a request's input (cached included) passes
 *  `threshold` tokens, the whole request is billed at these prices. */
export type ContextTier = {
  cache_read?: number;
  cache_write_1h?: number;
  cache_write_5m?: number;
  input?: number;
  output?: number;
  threshold: number;
};

export type CatalogPricing = {
  image_pricing?: ImagePricing;
  context_tiers?: ContextTier[];
  peak_pricing?: CatalogPricing;
  context_tier?: ContextTier | null;
  audio_input?: number;
  audio_output?: number;
  billing_type?: string;
  cache_read?: number;
  cache_read_per_million?: number;
  cache_write_1h?: number;
  cache_write_1h_per_million?: number;
  cache_write_5m?: number;
  cache_write_5m_per_million?: number;
  image_input?: number;
  image_output?: number;
  input?: number;
  input_per_million?: number;
  output?: number;
  output_per_million?: number;
  request?: number;
  request_price?: number;
  second?: number;
  second_price?: number;
};

export type CatalogEndpoint = {
  method?: string;
  mode?: 'native' | 'bridged';
  path?: string;
  type?: string;
};

export type CatalogModel = {
  /** Wire model for the listed endpoint when it differs from the routing ID. */
  request_model?: string;
  decision_capabilities?: {
    question_types: string[];
    max_questions: number;
    max_images: number;
    max_state_tokens: number;
    max_state_bytes?: number;
    max_input_tokens: number;
    streaming: boolean;
  };
  accessible?: boolean;
  capabilities?: Record<string, boolean>;
  context_length?: number;
  context_window?: number;
  deprecation_date?: string;
  description?: string;
  description_en?: string;
  description_zh?: string;
  example?: { curl?: string };
  family?: string;
  health?: Lane['health'];
  id: string;
  input_limit?: number;
  input_modalities?: string[];
  is_featured?: boolean;
  is_new?: boolean;
  /** Released within the last two weeks — by release date, not listing day. */
  is_new_release?: boolean;
  knowledge_cutoff?: string;
  /** The lab behind the model; the catalog arrives grouped and ordered by it. */
  maker?: string;
  maker_rank?: number;
  lanes?: Lane[];
  max_output_tokens?: number;
  modality?: { input?: string[]; output?: string[] };
  name?: string;
  open_source?: boolean;
  output_modalities?: string[];
  owned_by?: string;
  pricing?: CatalogPricing;
  provider?: string;
  reasoning_options?: { efforts?: string[]; toggle?: boolean } | null;
  release_date?: string;
  released_at?: number;
  supported_endpoints?: CatalogEndpoint[];
  unsupported_params?: string[];
  tags?: string[];
  your_price?: CatalogPricing;
  your_prices_by_group?: Record<string, CatalogPricing>;
};

const asRecord = (value: unknown): Record<string, unknown> | null =>
  value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;

export const laneFromChannelGroup = (group?: string): LaneKey =>
  String(group || '')
    .trim()
    .toLowerCase() === 'cheap'
    ? 'eco'
    : 'stable';

export const readCatalogItems = (payload: unknown): CatalogModel[] => {
  if (Array.isArray(payload)) return payload as CatalogModel[];
  const root = asRecord(payload);
  const nested = root?.data;
  if (Array.isArray(nested)) return nested as CatalogModel[];
  const deeper = asRecord(nested)?.data;
  return Array.isArray(deeper) ? (deeper as CatalogModel[]) : [];
};

export const contextLengthOf = (model: CatalogModel) => {
  const value = model.context_length || model.context_window;
  return value && value > 0 ? value : undefined;
};

/** "1M", not "1000k". Vendors publish million-token windows and readers
 *  compare them at a glance; four digits of kilo-tokens reads as noise. */
export const formatContextLength = (tokens: number) => {
  if (tokens >= 1_000_000) {
    // 1,048,576 and 1,000,000 are both "1M" to a reader comparing windows,
    // but 1,050,000 is a different, published window: "1.05M".
    if (tokens % 1_048_576 === 0) return `${tokens / 1_048_576}M`;
    const millions = Math.round((tokens / 1_000_000) * 100) / 100;
    return `${millions}M`;
  }
  return `${Math.round(tokens / 1000)}k`;
};

/** Chinese locales prefer the zh copy; everything else falls back through
 *  en → generic so a partially localized catalog still shows something. */
export const descriptionOf = (model: CatalogModel, language: string) => {
  const zhFirst = language.toLowerCase().startsWith('zh');
  const ordered = zhFirst
    ? [model.description_zh, model.description_en, model.description]
    : [model.description_en, model.description, model.description_zh];
  return ordered.find((text) => text && text.trim()) || '';
};

export const modalitiesOf = (model: CatalogModel) => ({
  input: model.modality?.input?.length
    ? model.modality.input
    : model.input_modalities || [],
  output: model.modality?.output?.length
    ? model.modality.output
    : model.output_modalities || [],
});

export const formatTokenWindow = (value?: number) => {
  if (!value || value <= 0) return null;
  if (value >= 1_000_000) {
    if (value % 1_048_576 === 0) return `${value / 1_048_576}M`;
    return `${Math.round((value / 1_000_000) * 100) / 100}M`;
  }
  if (value >= 1000) return `${Math.round(value / 1000)}k`;
  return String(value);
};

export const listPriceOf = (pricing?: CatalogPricing) => ({
  input: pricing?.input_per_million ?? pricing?.input,
  output: pricing?.output_per_million ?? pricing?.output,
  perCall: pricing?.request ?? pricing?.request_price,
  perSecond: pricing?.second ?? pricing?.second_price,
});

/** The lane's standard price as a share of the model's official list price:
 *  0.23 reads "2.3折". It is the lane's own discount, the same for every
 *  customer; a 专属折扣 is shown beside it, never multiplied into it. The
 *  billing unit the lane leads with decides the pair (per call, per second,
 *  else input). Undefined when either side is missing — no mark is better
 *  than a guessed one. */
export const laneOfficialRatio = (
  lane: Lane,
  model: CatalogModel,
): number | undefined => {
  const official = listPriceOf(model.pricing);
  const list = laneListPrice(lane, model);
  for (const key of ['perCall', 'perSecond', 'input', 'output'] as const) {
    const paid = list[key];
    const base = official[key];
    if (paid != null && base != null && base > 0) return paid / base;
  }
  return undefined;
};

/** A 专属折扣 as 折: 40% off reads 6. */
export const planZheOf = (lane: Lane): number | undefined =>
  lane.plan_discount_pct != null && lane.plan_discount_pct > 0
    ? Math.round((100 - lane.plan_discount_pct) * 10) / 100
    : undefined;

/** The model's official price for the dimension a hero price leads with. */
export const officialHeroPrice = (
  model: CatalogModel,
  kind: HeroPrice['kind'],
): number | undefined => listPriceOf(model.pricing)[kind];

export type DiscountMark =
  | { kind: 'list' }
  | { kind: 'off'; off: number; zhe: number };

/** The lane's discount mark: the official price itself, or how far under it.
 *  A lane dearer than the list price carries no mark. */
export const discountMarkOf = (ratio?: number): DiscountMark | null => {
  if (ratio == null || !Number.isFinite(ratio)) return null;
  if (Math.abs(ratio - 1) < 0.005) return { kind: 'list' };
  if (ratio > 1) return null;
  return {
    kind: 'off',
    off: Math.round((1 - ratio) * 100),
    zhe: Math.round(ratio * 100) / 10,
  };
};

export const healthTone = (health?: Lane['health']) => {
  const status = String(health?.status || '').toLowerCase();
  if (
    status === 'unavailable' ||
    status === 'down' ||
    status === 'cooldown' ||
    status === 'circuit_open'
  ) {
    return 'bad';
  }
  if (status === 'degraded' || status === 'warning') return 'warn';
  // The status is the router's own verdict on the lane right now. The window's
  // availability still carries the morning's outage after the lane recovered,
  // so it only decides the tone when no verdict was published.
  if (status === 'available' || status === 'ok' || status === 'healthy') {
    return 'ok';
  }
  if (health?.availability == null) return 'neutral';
  if (health.availability >= 0.99) return 'ok';
  if (health.availability >= 0.97) return 'warn';
  return 'bad';
};

/** True when the current UTC hour falls inside a "1-4,6-10"-style window.
 *  Malformed input reads as "no window" — better to show the base price
 *  than to invent a surcharge from a string we could not parse. */
export const peakActiveNow = (
  hours?: string | null,
  now: Date = new Date(),
  calendar?: PeakCalendar | null,
): boolean => {
  if (!hours) return false;
  if (calendar) {
    const local = new Date(now.getTime() + 8 * 60 * 60 * 1000);
    const weekday = local.getUTCDay();
    if (
      !calendar.years.includes(local.getUTCFullYear()) ||
      weekday === 0 ||
      weekday === 6 ||
      calendar.holidays.includes(local.toISOString().slice(0, 10))
    )
      return false;
  }
  const hour = now.getUTCHours();
  return hours.split(',').some((part) => {
    const range = part.trim();
    if (!range) return false;
    const [startRaw, endRaw] = range.split('-');
    const start = Number(startRaw);
    const end = endRaw === undefined ? start : Number(endRaw);
    if (
      !Number.isInteger(start) ||
      !Number.isInteger(end) ||
      start < 0 ||
      start > 23 ||
      end < 0 ||
      end > 24 ||
      start === end
    ) {
      return false;
    }
    // Wrapping windows (22-2) cross midnight.
    return start < end
      ? hour >= start && hour < end
      : hour >= start || hour < end;
  });
};

export const lanePeakFactor = (lane: Lane, now: Date = new Date()) =>
  peakActiveNow(lane.peak_hours_utc, now, lane.peak_calendar) &&
  (lane.peak_multiplier ?? 0) > 0
    ? (lane.peak_multiplier as number)
    : 1;

export const applyLanePeak = (
  value: number | undefined,
  lane: Lane,
  now: Date = new Date(),
) => (value == null ? undefined : value * lanePeakFactor(lane, now));

/** New catalogs supply the independently calculated peak contract price. */
export const personalPricing = (lane: Lane, now: Date = new Date()) =>
  lanePeakFactor(lane, now) !== 1 && lane.your_price?.peak_pricing
    ? lane.your_price.peak_pricing
    : lane.your_price;

export const applyPersonalPeak = (
  value: number | undefined,
  lane: Lane,
  now: Date = new Date(),
) => (lane.your_price?.peak_pricing ? value : applyLanePeak(value, lane, now));

export const laneListPrice = (lane: Lane, model: CatalogModel) => {
  const price = listPriceOf(lane.pricing);
  const fallback = listPriceOf(model.pricing);
  const stableFallback = lane.key === 'stable';
  return {
    input:
      price.input ??
      lane.input_per_million ??
      (stableFallback ? fallback.input : undefined),
    output:
      price.output ??
      lane.output_per_million ??
      (stableFallback ? fallback.output : undefined),
    perCall:
      price.perCall ??
      lane.per_call ??
      (stableFallback ? fallback.perCall : undefined),
    perSecond:
      price.perSecond ??
      lane.per_second ??
      (stableFallback ? fallback.perSecond : undefined),
  };
};

export type HeroPrice = {
  kind: 'perCall' | 'perSecond' | 'input' | 'output';
  list: number;
  listOut?: number;
  mine?: number;
  mineOut?: number;
};

/** The one number a card leads with, in the order a reader expects: a whole
 *  per-call or per-second price if the lane has one, otherwise input with
 *  output trailing it. A lane priced only on output still has a price, so it
 *  leads with that instead of leaving the card with no price at all. */
export const heroPriceOf = (
  lane: Lane,
  model: CatalogModel,
  now: Date = new Date(),
): HeroPrice | null => {
  const list = laneListPrice(lane, model);
  const mine = listPriceOf(personalPricing(lane, now));
  const peak = (value?: number) => applyLanePeak(value, lane, now);
  const personal = (value?: number) => applyPersonalPeak(value, lane, now);
  const listOut = peak(list.output);
  const mineOut = personal(mine.output);
  if (peak(list.perCall) != null)
    return {
      kind: 'perCall',
      list: peak(list.perCall) as number,
      mine: personal(mine.perCall),
    };
  if (peak(list.perSecond) != null)
    return {
      kind: 'perSecond',
      list: peak(list.perSecond) as number,
      mine: personal(mine.perSecond),
    };
  if (peak(list.input) != null)
    return {
      kind: 'input',
      list: peak(list.input) as number,
      listOut,
      mine: personal(mine.input),
      mineOut,
    };
  if (listOut != null) return { kind: 'output', list: listOut, mine: mineOut };
  return null;
};

export const resolveLanes = (model: CatalogModel): Lane[] => {
  if (model.lanes?.length) {
    return model.lanes.map((lane) => ({
      ...lane,
      your_price:
        lane.your_price ??
        model.your_prices_by_group?.[lane.key === 'eco' ? 'cheap' : 'direct'],
    }));
  }
  const list = listPriceOf(model.pricing);
  return [
    {
      available: true,
      health: model.health,
      input_per_million: list.input,
      key: 'stable',
      output_per_million: list.output,
      per_call: list.perCall,
      per_second: list.perSecond,
      your_price: model.your_price,
    },
  ];
};

/** The model's release day, YYYY-MM-DD. */
export const releaseDateOf = (model: CatalogModel) =>
  model.release_date ||
  (model.released_at
    ? new Date(model.released_at * 1000).toISOString().slice(0, 10)
    : undefined);

/** "272K", "1M": how a context threshold reads in a sentence. */
export const formatTierThreshold = (tokens: number) => {
  if (tokens >= 1_000_000 && tokens % 100_000 === 0) {
    return `${tokens / 1_000_000}M`;
  }
  return `${Math.round(tokens / 1000)}K`;
};

/** A lane's long-context tier as a reader pays it: the viewer's own price
 *  when they have one, the list price otherwise. */
export const laneContextTiers = (
  lane: Lane,
  now: Date = new Date(),
): ContextTier[] => {
  const mine = personalPricing(lane, now);
  const price = mine ?? lane.pricing;
  const tiers =
    price?.context_tiers ?? (price?.context_tier ? [price.context_tier] : []);
  const scale = (value?: number) =>
    mine
      ? applyPersonalPeak(value, lane, now)
      : applyLanePeak(value, lane, now);
  return tiers.map((tier) => ({
    ...tier,
    input: scale(tier.input ?? price?.input),
    output: scale(tier.output ?? price?.output),
    cache_read: scale(tier.cache_read ?? price?.cache_read),
    cache_write_5m: scale(tier.cache_write_5m ?? price?.cache_write_5m),
    cache_write_1h: scale(tier.cache_write_1h ?? price?.cache_write_1h),
  }));
};

export const laneContextTier = (lane: Lane): ContextTier | undefined =>
  laneContextTiers(lane)[0];

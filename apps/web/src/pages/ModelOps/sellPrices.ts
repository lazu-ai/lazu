/** Shape and write rules for `/api/pricing/sell_prices`.
 *
 *  The endpoint is a versioned full-row upsert: it deactivates the current row
 *  and creates a new one straight from the payload, so any field the payload
 *  omits comes back null. This module owns the merge that keeps the dimensions
 *  the form never shows (cache, audio, image) alive across a save. */
/** How a model bills. The form swaps its price fields on this, because a
 *  per-call image model has no input/output token price to show. */
import type { ImagePricing } from '../../lib/image-pricing';
import type { VideoPricing } from '../../lib/video-pricing';

export type BillingMode = 'per_call' | 'per_second' | 'per_token';

export type LaneKey = 'eco' | 'stable';

export type LanePrice = {
  image_pricing?: ImagePricing;
  video_pricing?: VideoPricing | null;
  cache_read_per_mtok?: number | null;
  cache_write_5m_per_mtok?: number | null;
  cache_write_1h_per_mtok?: number | null;
  billing_mode?: BillingMode;
  enabled?: boolean;
  input_per_million?: number | null;
  output_per_million?: number | null;
  peak_hours_utc?: string | null;
  peak_multiplier?: number | null;
  per_call?: number | null;
  per_second?: number | null;
  /** Long-context pricing: past this many input tokens (cache included) the
   *  whole request is billed at the context_tier_* prices. 0 = single price. */
  context_tier_threshold?: number | null;
  context_tier_input_per_mtok?: number | null;
  context_tier_output_per_mtok?: number | null;
  context_tier_cache_read_per_mtok?: number | null;
  context_tier_cache_write_5m_per_mtok?: number | null;
  context_tier_cache_write_1h_per_mtok?: number | null;
  /** The lane is priced as a percent of the official price (see
   *  ratioPricing.ts); price_ratio null = the lane's default ratio. */
  follow_official?: boolean;
  price_ratio?: number | null;
  pinned_fields?: string | null;
};

export type PricedModel = {
  cost_input_per_million?: number | null;
  cost_output_per_million?: number | null;
  eco?: LanePrice;
  model_name: string;
  stable?: LanePrice;
  tier_thresholds?: string | null;
};

export const emptyLane = (): LanePrice => ({
  billing_mode: 'per_token',
  enabled: false,
});

export type OptionalNumberResult = {
  valid: boolean;
  value: number | null;
};

export const parseOptionalNumber = (
  raw: string,
  badInput = false,
): OptionalNumberResult => {
  if (badInput) return { valid: false, value: null };
  const trimmed = raw.trim();
  if (trimmed === '') return { valid: true, value: null };
  const value = Number(trimmed);
  return Number.isFinite(value)
    ? { valid: true, value }
    : { valid: false, value: null };
};

/** A row exactly as `/api/pricing/sell_prices` returns it. The upsert replaces
 *  the whole row, so we keep the raw record and merge edits onto it — otherwise
 *  saving a token price would null the cache / audio / image dimensions this
 *  form never shows, and image models would silently bill $0. */
export type SellPriceRow = LanePrice & {
  active?: boolean;
  channel_group?: string;
  created_at?: string;
  id?: number;
  input_per_mtok?: number | null;
  model_name: string;
  output_per_mtok?: number | null;
  pricing_version?: number;
  request_usd?: number | null;
  second_usd?: number | null;
  source?: string;
  updated_at?: string;
};

export const findSellPriceRow = (
  rows: SellPriceRow[],
  requestedModelName: string,
  lane: 'cheap' | 'direct',
  pricingModelName?: string,
) => {
  const owner = pricingModelName || requestedModelName;
  return rows.find(
    (row) =>
      row.model_name === owner && normalizeLaneSlug(row.channel_group) === lane,
  );
};

export const billingModeOf = (row: SellPriceRow): BillingMode => {
  if (row.image_pricing || row.request_usd != null) return 'per_call';
  if (row.second_usd != null) return 'per_second';
  return 'per_token';
};

export const toLane = (row: SellPriceRow): LanePrice => ({
  image_pricing: row.image_pricing,
  video_pricing: row.video_pricing ?? null,
  billing_mode: billingModeOf(row),
  enabled: row.active !== false,
  input_per_million: row.input_per_mtok ?? row.input_per_million ?? null,
  output_per_million: row.output_per_mtok ?? row.output_per_million ?? null,
  cache_read_per_mtok: row.cache_read_per_mtok,
  cache_write_5m_per_mtok: row.cache_write_5m_per_mtok,
  cache_write_1h_per_mtok: row.cache_write_1h_per_mtok,
  peak_hours_utc: row.peak_hours_utc ?? null,
  peak_multiplier: row.peak_multiplier ?? null,
  per_call: row.request_usd ?? row.per_call ?? null,
  per_second: row.second_usd ?? row.per_second ?? null,
  context_tier_threshold: row.context_tier_threshold ?? null,
  context_tier_input_per_mtok: row.context_tier_input_per_mtok ?? null,
  context_tier_output_per_mtok: row.context_tier_output_per_mtok ?? null,
  context_tier_cache_read_per_mtok:
    row.context_tier_cache_read_per_mtok ?? null,
  context_tier_cache_write_5m_per_mtok:
    row.context_tier_cache_write_5m_per_mtok ?? null,
  context_tier_cache_write_1h_per_mtok:
    row.context_tier_cache_write_1h_per_mtok ?? null,
  follow_official: Boolean(row.follow_official),
  price_ratio: row.price_ratio ?? null,
  pinned_fields: row.pinned_fields ?? '',
});

export const groupSellPrices = (rows: SellPriceRow[]): PricedModel[] => {
  const byName = new Map<string, PricedModel>();
  const seen = new Set<string>();
  rows.forEach((row) => {
    const lane = row.channel_group === 'cheap' ? 'eco' : 'stable';
    const laneKey = `${row.model_name}:${lane}`;
    if (seen.has(laneKey)) return;
    seen.add(laneKey);
    const current = byName.get(row.model_name) || {
      model_name: row.model_name,
      eco: emptyLane(),
      stable: emptyLane(),
    };
    current[lane] = toLane(row);
    byName.set(row.model_name, current);
  });
  return [...byName.values()];
};

/** Price fields the form edits. Everything else on the row is carried over. */
export const editedFields = (
  value: LanePrice,
  mode: BillingMode,
): Record<string, number | string | null> => ({
  input_per_mtok:
    mode === 'per_token' ? (value.input_per_million ?? null) : null,
  output_per_mtok:
    mode === 'per_token' ? (value.output_per_million ?? null) : null,
  request_usd: mode === 'per_call' ? (value.per_call ?? null) : null,
  second_usd: mode === 'per_second' ? (value.per_second ?? null) : null,
  ...(value.cache_read_per_mtok !== undefined
    ? { cache_read_per_mtok: value.cache_read_per_mtok }
    : {}),
  ...(value.cache_write_5m_per_mtok !== undefined
    ? { cache_write_5m_per_mtok: value.cache_write_5m_per_mtok }
    : {}),
  ...(value.cache_write_1h_per_mtok !== undefined
    ? { cache_write_1h_per_mtok: value.cache_write_1h_per_mtok }
    : {}),
  peak_hours_utc: value.peak_hours_utc || '',
  peak_multiplier: value.peak_multiplier ?? null,
  // A tier only exists on token pricing, and only with a threshold.
  ...contextTierFields(value, mode),
});

/** The long-context columns as the row stores them. Without a threshold the
 *  tier prices are cleared too, so a switched-off tier leaves nothing behind. */
export const contextTierFields = (
  value: LanePrice,
  mode: BillingMode,
): Record<string, number | null> => {
  const threshold =
    mode === 'per_token' && (value.context_tier_threshold ?? 0) > 0
      ? (value.context_tier_threshold as number)
      : 0;
  const keep = (price?: number | null) =>
    threshold > 0 ? (price ?? null) : null;
  return {
    context_tier_threshold: threshold,
    context_tier_input_per_mtok: keep(value.context_tier_input_per_mtok),
    context_tier_output_per_mtok: keep(value.context_tier_output_per_mtok),
    context_tier_cache_read_per_mtok: keep(
      value.context_tier_cache_read_per_mtok,
    ),
    context_tier_cache_write_5m_per_mtok: keep(
      value.context_tier_cache_write_5m_per_mtok,
    ),
    context_tier_cache_write_1h_per_mtok: keep(
      value.context_tier_cache_write_1h_per_mtok,
    ),
  };
};

export const laneBillingMode = (
  model: PricedModel,
  lane: LaneKey,
): BillingMode => model[lane]?.billing_mode || 'per_token';

export const lanePayload = (
  model: PricedModel,
  lane: LaneKey,
  current?: SellPriceRow,
): Record<string, unknown> => {
  const base: Record<string, unknown> = { ...(current || {}) };
  // Identity and versioning belong to the server: it deactivates the previous
  // row and creates a new version, so echoing them back would collide.
  delete base.id;
  delete base.pricing_version;
  delete base.created_at;
  delete base.updated_at;
  delete base.enabled;
  return {
    ...base,
    ...editedFields(model[lane] || emptyLane(), laneBillingMode(model, lane)),
    model_name: model.model_name,
    channel_group: lane === 'eco' ? 'cheap' : 'direct',
    active: true,
    source: 'manual',
  };
};

/** True when any edited price on this lane is negative. Zero is a legal
 *  price (free lane); negative would credit customers per request, and the
 *  backend upsert does not validate prices either — the console is the
 *  gate. */
export const laneHasNegativePrice = (
  value: LanePrice | undefined,
  mode: BillingMode,
) => {
  const fields = editedFields(value || emptyLane(), mode);
  return (
    [
      fields.input_per_mtok,
      fields.output_per_mtok,
      fields.request_usd,
      fields.second_usd,
      fields.peak_multiplier,
    ] as Array<number | string | null>
  ).some((field) => typeof field === 'number' && field < 0);
};

export const laneHasPrice = (
  value: LanePrice | undefined,
  mode: BillingMode,
) => {
  const fields = editedFields(value || emptyLane(), mode);
  return (
    fields.input_per_mtok != null ||
    fields.output_per_mtok != null ||
    fields.request_usd != null ||
    fields.second_usd != null
  );
};

/** Every save publishes a new pricing version, so only write a lane the
 *  operator actually moved. */
export const laneChanged = (
  model: PricedModel,
  lane: LaneKey,
  current?: SellPriceRow,
) => {
  const mode = laneBillingMode(model, lane);
  const next = editedFields(model[lane] || emptyLane(), mode);
  const previous = current
    ? editedFields(toLane(current), billingModeOf(current))
    : null;
  if (!previous) return true;
  return (Object.keys(next) as (keyof typeof next)[]).some(
    (key) => (next[key] ?? null) !== (previous[key] ?? null),
  );
};

/** The historical aliases for 稳定 collapse onto it; every other slug is kept
 *  as written. Lanes come from a registry an operator can add to, so folding an
 *  unknown one onto `direct` would attribute its price to the wrong lane. */
export const normalizeLaneSlug = (raw?: string | null): string => {
  const value = String(raw ?? '')
    .trim()
    .toLowerCase();
  if (value === '' || value === 'default' || value === 'auto') return 'direct';
  return value;
};

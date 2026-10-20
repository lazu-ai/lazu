import {
  billingModeOf,
  editedFields,
  parseOptionalNumber,
  toLane,
  type BillingMode,
  type LanePrice,
  type SellPriceRow,
} from './sellPrices';
import { readImagePricing, type ImagePricing } from '../../lib/image-pricing';
import type { VideoPricing } from '../../lib/video-pricing';
/** Pricing a lane as a percent of the official price.
 *
 *  The server owns the arithmetic: a row saved with `follow_official` has its
 *  unpinned token prices derived there from the model's official price × the
 *  ratio (the row's own, else the lane's), and re-derived whenever either
 *  moves. This module mirrors that so the dialog can show what a ratio will
 *  charge before it is saved, and builds the row the save sends. */

/** The model's official list price, USD per million tokens. */
export type OfficialPrice = {
  cacheRead: number | null;
  cacheWrite5m: number | null;
  input: number | null;
  output: number | null;
  tierThreshold: number;
};

/** Official input/output columns hold USD per token (LiteLLM's unit), but
 *  some rows were typed per million; anything at or above $0.001 per token
 *  can only be a per-million figure. Same rule as the backend. */
export const officialPerMtok = (value: unknown): number | null => {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) return null;
  return number < 0.001 ? number * 1e6 : number;
};

export const officialOf = (
  record: Record<string, unknown> | undefined,
): OfficialPrice | null => {
  if (!record) return null;
  const input = officialPerMtok(record.official_input_price);
  const output = officialPerMtok(record.official_output_price);
  if (input == null && output == null) return null;
  const positive = (value: unknown) => {
    const number = Number(value);
    return Number.isFinite(number) && number > 0 ? number : null;
  };
  const tier = record.official_context_tier as
    | { threshold?: number }
    | undefined;
  return {
    cacheRead: positive(record.official_cache_read_per_mtok),
    cacheWrite5m: positive(record.official_cache_write_per_mtok),
    input,
    output,
    tierThreshold: Number(tier?.threshold) || 0,
  };
};

/** The token prices an operator can pin while the rest follows. */
export type PinKey = 'cache_read' | 'cache_write_5m' | 'input' | 'output';
export const PIN_KEYS: PinKey[] = [
  'input',
  'output',
  'cache_read',
  'cache_write_5m',
];

const rowField: Record<PinKey, keyof SellPriceRow> = {
  cache_read: 'cache_read_per_mtok',
  cache_write_5m: 'cache_write_5m_per_mtok',
  input: 'input_per_mtok',
  output: 'output_per_mtok',
};

const officialField: Record<PinKey, keyof OfficialPrice> = {
  cache_read: 'cacheRead',
  cache_write_5m: 'cacheWrite5m',
  input: 'input',
  output: 'output',
};

export type LaneDraft = {
  imagePricing?: ImagePricing;
  /** Video price JSON as typed; '' = none. Parsed on save. */
  videoPricing: string;
  billingMode: BillingMode;
  cacheRead: string;
  cacheWrite1h: string;
  cacheWrite5m: string;
  /** False until the lane has a price row or the operator opens it. */
  exists: boolean;
  input: string;
  /** A tier pinned through the API stays pinned; the dialog cannot pin one. */
  keepTierPin: boolean;
  mode: 'fixed' | 'follow';
  output: string;
  peakHours: string;
  peakMultiplier: string;
  perCall: string;
  perSecond: string;
  /** Pinned token prices, by key; a key present is pinned. */
  pins: Partial<Record<PinKey, string>>;
  /** Percent of the official price; '' = the lane's default. */
  ratio: string;
  weights: Record<string, string>;
};

const text = (value: unknown) =>
  value === null || value === undefined || value === '' ? '' : String(value);

export const pinnedSet = (raw?: string | null) =>
  new Set(
    String(raw || '')
      .split(',')
      .map((part) => part.trim())
      .filter(Boolean),
  );

export const draftFromRow = (
  row: SellPriceRow | undefined,
  weights: Record<string, string>,
  official: OfficialPrice | null,
): LaneDraft => {
  const pinned = pinnedSet(row?.pinned_fields);
  const pins: Partial<Record<PinKey, string>> = {};
  if (row?.follow_official) {
    PIN_KEYS.forEach((key) => {
      if (pinned.has(key)) pins[key] = text(row[rowField[key]]);
    });
  }
  return {
    imagePricing: row?.image_pricing ? { ...row.image_pricing } : undefined,
    videoPricing: row?.video_pricing
      ? JSON.stringify(row.video_pricing, null, 2)
      : '',
    billingMode: row ? billingModeOf(row) : 'per_token',
    cacheRead: text(row?.cache_read_per_mtok),
    cacheWrite1h: text(row?.cache_write_1h_per_mtok),
    cacheWrite5m: text(row?.cache_write_5m_per_mtok),
    exists: Boolean(row),
    input: text(row?.input_per_mtok),
    keepTierPin: Boolean(row?.follow_official && pinned.has('context_tier')),
    mode: row
      ? row.follow_official
        ? 'follow'
        : 'fixed'
      : official
        ? 'follow'
        : 'fixed',
    output: text(row?.output_per_mtok),
    peakHours: row?.peak_hours_utc || '',
    peakMultiplier: text(row?.peak_multiplier),
    perCall: text(row?.request_usd),
    perSecond: text(row?.second_usd),
    pins,
    ratio:
      row?.follow_official && row.price_ratio != null
        ? text(row.price_ratio)
        : '',
    weights,
  };
};

const round = (value: number) => Math.round(value * 1e6) / 1e6;

const numberOrNull = (raw: string) => {
  const parsed = parseOptionalNumber(raw);
  return parsed.valid ? parsed.value : null;
};

/** The ratio a following lane is priced at, as a percent. */
export const effectiveRatio = (draft: LaneDraft, laneRatio: number) => {
  const own = numberOrNull(draft.ratio);
  return own != null && own > 0 ? own : laneRatio > 0 ? laneRatio : 100;
};

export type DerivedPrice = Record<PinKey, number | null>;

/** What the lane will charge per million tokens once saved. `current` stands
 *  in for a field the official price does not carry: the server keeps the
 *  row's own value there too. */
export const derivedPrice = (
  draft: LaneDraft,
  official: OfficialPrice | null,
  laneRatio: number,
  current?: SellPriceRow,
): DerivedPrice => {
  const out = {} as DerivedPrice;
  if (draft.mode === 'fixed' || !official) {
    out.input = numberOrNull(draft.input);
    out.output = numberOrNull(draft.output);
    out.cache_read = numberOrNull(draft.cacheRead);
    out.cache_write_5m = numberOrNull(draft.cacheWrite5m);
    return out;
  }
  const factor = effectiveRatio(draft, laneRatio) / 100;
  PIN_KEYS.forEach((key) => {
    const pin = draft.pins[key];
    if (pin !== undefined) {
      out[key] = numberOrNull(pin);
      return;
    }
    const list = official[officialField[key]] as number | null;
    if (list != null && list > 0) {
      out[key] = round(list * factor);
      return;
    }
    const kept = current?.[rowField[key]];
    out[key] = typeof kept === 'number' ? kept : null;
  });
  return out;
};

/** The ratio a fixed price works out to, on input — what the ledger shows
 *  beside a fixed lane so it can be compared with the ones that follow. */
export const impliedRatio = (
  price: number | null | undefined,
  official: OfficialPrice | null,
) => {
  if (price == null || !official?.input) return null;
  return Math.round((price / official.input) * 1000) / 10;
};

export type DraftProblem = 'pin' | 'price' | 'ratio' | 'weight' | null;

/** The video price JSON parsed, or undefined when empty; null when invalid. */
export const readVideoPricingText = (
  raw: string,
): VideoPricing | null | undefined => {
  const text = raw.trim();
  if (!text) return undefined;
  try {
    const parsed = JSON.parse(text) as unknown;
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return null;
    }
    return parsed as VideoPricing;
  } catch {
    return null;
  }
};

export const validateDraft = (draft: LaneDraft): DraftProblem => {
  if (!draft.exists) return null;
  if (readVideoPricingText(draft.videoPricing) === null) return 'price';
  if (
    draft.imagePricing &&
    (!readImagePricing(draft.imagePricing) ||
      draft.mode === 'follow' ||
      draft.billingMode !== 'per_call')
  )
    return 'price';
  const nonNegative = (raw: string) => {
    const parsed = parseOptionalNumber(raw);
    return parsed.valid && (parsed.value == null || parsed.value >= 0);
  };
  if (draft.mode === 'follow') {
    if (draft.ratio.trim() !== '') {
      const ratio = parseOptionalNumber(draft.ratio);
      if (
        !ratio.valid ||
        ratio.value == null ||
        ratio.value <= 0 ||
        ratio.value > 1000
      ) {
        return 'ratio';
      }
    }
    if (
      Object.values(draft.pins).some(
        (pin) => pin === undefined || pin.trim() === '' || !nonNegative(pin),
      )
    ) {
      return 'pin';
    }
  } else {
    const fields =
      draft.billingMode === 'per_token'
        ? [
            draft.input,
            draft.output,
            draft.cacheRead,
            draft.cacheWrite5m,
            draft.cacheWrite1h,
          ]
        : draft.billingMode === 'per_call'
          ? [draft.perCall]
          : [draft.perSecond];
    if (![...fields, draft.peakMultiplier].every(nonNegative)) return 'price';
  }
  if (
    Object.values(draft.weights).some((weight) => !/^\d+$/.test(weight.trim()))
  ) {
    return 'weight';
  }
  return null;
};

/** The sell-price row the save sends for one lane. The row is replaced
 *  wholesale, so everything this dialog does not edit (audio, image, the
 *  long-context tier of a fixed price) is carried over from `current`. */
export const lanePayload = (
  draft: LaneDraft,
  current: SellPriceRow | undefined,
  lane: string,
  modelName: string,
): Record<string, unknown> => {
  const base: Record<string, unknown> = { ...(current || {}) };
  delete base.id;
  delete base.pricing_version;
  delete base.created_at;
  delete base.updated_at;
  delete base.enabled;
  if (draft.mode === 'follow') {
    const pinned = PIN_KEYS.filter((key) => draft.pins[key] !== undefined);
    const payload: Record<string, unknown> = {
      ...base,
      follow_official: true,
      pinned_fields: [
        ...pinned,
        ...(draft.keepTierPin ? ['context_tier'] : []),
      ].join(','),
      price_ratio: numberOrNull(draft.ratio),
      // A price that follows is a token price.
      request_usd: null,
      second_usd: null,
    };
    pinned.forEach((key) => {
      payload[rowField[key]] = numberOrNull(draft.pins[key] as string);
    });
    return {
      ...payload,
      active: true,
      channel_group: lane,
      model_name: current?.model_name || modelName,
      source: 'manual',
    };
  }
  const value: LanePrice = {
    ...(current ? toLane(current) : {}),
    cache_read_per_mtok: numberOrNull(draft.cacheRead),
    cache_write_5m_per_mtok: numberOrNull(draft.cacheWrite5m),
    cache_write_1h_per_mtok: numberOrNull(draft.cacheWrite1h),
    input_per_million: numberOrNull(draft.input),
    output_per_million: numberOrNull(draft.output),
    peak_hours_utc: draft.peakHours.trim(),
    peak_multiplier: numberOrNull(draft.peakMultiplier),
    per_call: numberOrNull(draft.perCall),
    per_second: numberOrNull(draft.perSecond),
  };
  return {
    ...base,
    ...editedFields(value, draft.billingMode),
    ...(draft.imagePricing
      ? {
          image_pricing: draft.imagePricing,
          request_usd: draft.imagePricing.output_usd,
        }
      : {}),
    video_pricing: readVideoPricingText(draft.videoPricing) ?? null,
    active: true,
    channel_group: lane,
    follow_official: false,
    model_name: current?.model_name || modelName,
    pinned_fields: '',
    price_ratio: null,
    source: 'manual',
  };
};

/** True when saving this lane would publish a different price. */
export const lanePriceChanged = (initial: LaneDraft, draft: LaneDraft) => {
  if (!draft.exists) return false;
  if (!initial.exists) return true;
  const priceOnly = (value: LaneDraft) =>
    JSON.stringify({ ...value, weights: undefined });
  return priceOnly(initial) !== priceOnly(draft);
};

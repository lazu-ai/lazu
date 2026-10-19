export type ModelPricingRecord = Record<string, unknown> & {
  brand_icon?: unknown;
  has_price?: unknown;
  icon?: unknown;
  input_per_mtok?: unknown;
  model_name?: unknown;
  owner_by?: unknown;
  output_per_mtok?: unknown;
  pricing_status?: unknown;
  request_usd?: unknown;
  second_usd?: unknown;
  vendor_icon?: unknown;
  vendor_name?: unknown;
};

const PRICE_FIELDS = [
  'request_usd',
  'second_usd',
  'input_per_mtok',
  'output_per_mtok',
  'cache_read_per_mtok',
  'cache_write_5m_per_mtok',
  'cache_write_1h_per_mtok',
  'audio_input_per_mtok',
  'audio_output_per_mtok',
  'image_input_per_mtok',
  'image_output_per_mtok',
] as const;

const BRAND_ICON_BY_OWNER: Record<string, string> = {
  anthropic: 'Claude.Color',
  claude: 'Claude.Color',
  gemini: 'Gemini.Color',
  google: 'Gemini.Color',
  openai: 'OpenAI',
  xai: 'XAI',
};

const OWNER_BY_MODEL_PATTERN: Array<{ pattern: RegExp; owner: string }> = [
  { pattern: /gemini|google|vertex/i, owner: 'Google' },
  { pattern: /claude|anthropic/i, owner: 'Anthropic' },
  { pattern: /dall-e|whisper|gpt-|^gpt|^o[134]\b|openai/i, owner: 'OpenAI' },
  { pattern: /grok|xai/i, owner: 'xAI' },
];

const stringValue = (value: unknown): string | null => {
  if (typeof value !== 'string') return null;
  const trimmed = value.trim();
  return trimmed === '' ? null : trimmed;
};

const numericValue = (value: unknown): number | null => {
  if (value === null || value === undefined || value === '') return null;
  const n = Number(value);
  return Number.isFinite(n) ? n : null;
};

export const hasConfiguredModelPrice = (
  record?: ModelPricingRecord | null,
): boolean => {
  if (!record) return false;
  if (record.has_price === true) return true;
  if (record.has_price === false || record.pricing_status === 'missing') {
    return false;
  }
  return PRICE_FIELDS.some((field) => numericValue(record[field]) !== null);
};

export const getPrimaryModelPrice = (
  record?: ModelPricingRecord | null,
): number | null => {
  if (!hasConfiguredModelPrice(record)) return null;
  for (const field of PRICE_FIELDS) {
    const value = numericValue(record?.[field]);
    if (value !== null) return value;
  }
  return null;
};

export const inferModelOwner = (
  record?: ModelPricingRecord | null,
): string | null => {
  if (!record) return null;

  const modelName = stringValue(record.model_name) || '';
  for (const rule of OWNER_BY_MODEL_PATTERN) {
    if (rule.pattern.test(modelName)) return rule.owner;
  }
  const explicitOwner = stringValue(record.owner_by);
  if (explicitOwner) return explicitOwner;
  return stringValue(record.vendor_name);
};

export const resolveModelBrand = (
  record?: ModelPricingRecord | null,
): { iconKey?: string; name?: string } => {
  const owner = inferModelOwner(record) || undefined;
  const ownerKey = owner?.toLowerCase().replace(/[^a-z0-9]/g, '');
  const inferredIcon = ownerKey ? BRAND_ICON_BY_OWNER[ownerKey] : undefined;
  const iconKey =
    stringValue(record?.brand_icon) ||
    inferredIcon ||
    stringValue(record?.icon) ||
    stringValue(record?.vendor_icon) ||
    undefined;

  return {
    iconKey,
    name: owner || stringValue(record?.model_name) || undefined,
  };
};

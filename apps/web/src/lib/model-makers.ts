import {
  resolveProviderBrand,
  type ProviderBrand,
} from '../components/ui/provider-brands';
import type { TFunction } from 'i18next';

/**
 * A model maker is the lab behind a model (Anthropic, OpenAI, DeepSeek…),
 * named by the key the backend sends as `maker`. The backend also sends every
 * list already in display order — the operator's maker order, then newest
 * release first — so pages group rows by maker and never re-sort them.
 */

/** A string each lab's brand pattern recognises, for its logo. */
const MAKER_BRAND_HINT: Record<string, string> = {
  alibaba: 'alibaba',
  anthropic: 'anthropic',
  bytedance: 'seed',
  cohere: 'cohere',
  deepseek: 'deepseek',
  google: 'google',
  meta: 'llama',
  minimax: 'minimax',
  mistral: 'mistral',
  moonshot: 'moonshot',
  openai: 'openai',
  stepfun: 'stepfun',
  xai: 'grok',
  xiaomi: 'xiaomi',
  zhipu: 'zhipu',
};

/** How a maker is named where customers read it: by the model line people
 *  know (Kimi, Qwen, 智谱) rather than the company's legal name. */
const MAKER_LABEL: Record<string, string> = {
  alibaba: 'Qwen',
  anthropic: 'Anthropic',
  bytedance: 'Seed',
  cohere: 'Cohere',
  deepseek: 'DeepSeek',
  google: 'Google',
  meta: 'Meta',
  minimax: 'MiniMax',
  mistral: 'Mistral',
  moonshot: 'Kimi',
  openai: 'OpenAI',
  stepfun: '阶跃星辰',
  xai: 'xAI',
  xiaomi: '小米',
  zhipu: '智谱',
};

/** The longer name the maker-order dialog uses, company and model line. */
const MAKER_FULL_LABEL: Record<string, string> = {
  bytedance: 'ByteDance · Seed',
  alibaba: '阿里云 · Qwen',
  moonshot: 'Moonshot AI · Kimi',
  zhipu: '智谱 · GLM',
};

export const makerDefaultLabel = (key: string) => MAKER_LABEL[key] || key;

export const makerLabel = (key: string, t: TFunction) =>
  key
    ? t(`page.console.makers.${key}`, { defaultValue: MAKER_LABEL[key] || key })
    : t('page.console.makers.other', { defaultValue: '其他' });

export const makerFullLabel = (key: string, t: TFunction) =>
  MAKER_FULL_LABEL[key]
    ? t(`page.console.makers.full.${key}`, {
        defaultValue: MAKER_FULL_LABEL[key],
      })
    : makerLabel(key, t);

/** The logo brand of a maker; unknown makers fall back to whatever the
 *  model's own fields resolve to. */
export const makerBrand = (
  key: string | undefined,
  fallback: { id?: string; name?: string; owned_by?: string } = {},
): ProviderBrand =>
  key && MAKER_BRAND_HINT[key]
    ? resolveProviderBrand({ name: MAKER_BRAND_HINT[key] })
    : resolveProviderBrand(fallback);

/** The order the product ships with — the server's default ModelMakerOrder.
 *  Used only when a response carries no maker rank. */
const DEFAULT_MAKER_ORDER = [
  'anthropic',
  'openai',
  'google',
  'deepseek',
  'moonshot',
  'zhipu',
  'alibaba',
  'minimax',
  'xai',
];

const BRAND_TO_MAKER: Record<string, string> = {
  'alibaba-cloud': 'alibaba',
  anthropic: 'anthropic',
  bytedance: 'bytedance',
  seed: 'bytedance',
  cohere: 'cohere',
  deepseek: 'deepseek',
  google: 'google',
  meta: 'meta',
  minimax: 'minimax',
  'mistral-ai': 'mistral',
  'moonshot-ai': 'moonshot',
  openai: 'openai',
  stepfun: 'stepfun',
  xai: 'xai',
  xiaomi: 'xiaomi',
  'zhipu-ai': 'zhipu',
};

/** The maker a row names, or one inferred from the model's own fields when
 *  the response predates makers (a cached catalog, an older API). */
export const makerOf = (
  maker: string | undefined,
  fallback: { id?: string; name?: string; owned_by?: string },
) => maker || BRAND_TO_MAKER[resolveProviderBrand(fallback).id] || '';

/** Groups rows by maker, keeping the row order the server sent. Groups are
 *  put in maker order — the server's rank when it sent one, the shipped
 *  default otherwise — so a list that arrives unsorted still reads right. */
export function groupByMaker<T>(
  rows: T[],
  makerOfRow: (row: T) => string,
  rankOf?: (row: T) => number | undefined,
): Array<{ maker: string; rows: T[] }> {
  const groups = new Map<string, T[]>();
  rows.forEach((row) => {
    const maker = makerOfRow(row) || '';
    groups.set(maker, [...(groups.get(maker) ?? []), row]);
  });
  const rank = (maker: string, first: T) => {
    const sent = rankOf?.(first);
    if (sent != null && Number.isFinite(sent)) return sent;
    const index = DEFAULT_MAKER_ORDER.indexOf(maker);
    return index < 0 ? DEFAULT_MAKER_ORDER.length : index;
  };
  return [...groups.entries()]
    .map(([maker, grouped], index) => ({
      index,
      maker,
      rank: maker ? rank(maker, grouped[0]) : Number.MAX_SAFE_INTEGER,
      rows: grouped,
    }))
    .sort((a, b) => a.rank - b.rank || a.index - b.index)
    .map(({ maker, rows: grouped }) => ({ maker, rows: grouped }));
}

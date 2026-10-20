import type { TFunction } from 'i18next';

/** Names and display forms for the model facts models.dev fills in. The keys
 *  match internal/model/model_info.go (ModelInfoField*). */

const ns = 'page.console.modelOps.info';

const FIELD_LABELS: Record<string, string> = {
  capabilities: '能力',
  context_length: '上下文',
  input_limit: '输入上限',
  knowledge_cutoff: '知识截止',
  maker: '厂商',
  max_output_tokens: '最大输出',
  modalities: '输入 / 输出',
  official_price: '官方价',
  official_cache_price: '官方缓存价（读 / 写）',
  official_context_tier: '官方长上下文价',
  open_weights: '开放权重',
  reasoning_options: '思考深度',
  release_date: '发布日期',
  unsupported_params: '不支持的参数',
};

export const infoFieldLabel = (field: string, t: TFunction) =>
  t(`${ns}.field.${field}`, { defaultValue: FIELD_LABELS[field] || field });

/** Where a fact came from, as the ledger and panel print it. A legacy
 *  release date is the listing day waiting for the first-sync decision. */
export const infoSourceLabel = (source: string | undefined, t: TFunction) => {
  switch (source) {
    case 'models.dev':
      return 'models.dev';
    case 'manual':
      return t(`${ns}.source.manual`, { defaultValue: '手填' });
    case 'legacy':
      return t(`${ns}.source.pending`, { defaultValue: '待确认' });
    case 'inferred':
      return t(`${ns}.source.inferred`, { defaultValue: '推断' });
    default:
      return '';
  }
};

const MODALITY_LABELS: Record<string, string> = {
  audio: '音频',
  image: '图片',
  pdf: 'PDF',
  text: '文本',
  video: '视频',
};

export const modalityLabel = (value: string, t: TFunction) =>
  t(`${ns}.modality.${value}`, {
    defaultValue: MODALITY_LABELS[value] || value,
  });

const groupDigits = (value: string) =>
  /^\d+$/.test(value) ? Number(value).toLocaleString('en-US') : value;

/** A queue value ("1050000", "2 / 10", "text,image -> text") as a reader
 *  wants to see it. */
export const formatInfoValue = (
  field: string,
  value: string,
  t: TFunction,
): string => {
  if (!value) return '—';
  switch (field) {
    case 'context_length':
    case 'input_limit':
    case 'max_output_tokens':
      return groupDigits(value);
    case 'modalities': {
      const [input, output] = value.split(' -> ');
      const names = (list = '') =>
        list
          .split(',')
          .filter(Boolean)
          .map((item) => modalityLabel(item, t))
          .join(' ');
      return `${names(input)} → ${names(output)}`;
    }
    case 'open_weights':
      return value === 'true'
        ? t(`${ns}.yes`, { defaultValue: '是' })
        : t(`${ns}.no`, { defaultValue: '否' });
    case 'unsupported_params':
      return value === 'none' ? t(`${ns}.none`, { defaultValue: '无' }) : value;
    case 'reasoning_options':
      // "toggle;low,high;mode=adaptive;off=never", each part optional
      return value
        .split(';')
        .filter(Boolean)
        .map((part) => {
          if (part === 'toggle')
            return t(`${ns}.thinkingToggle`, { defaultValue: '可关闭' });
          if (part.startsWith('mode='))
            return t(`${ns}.thinkingMode.${part.slice(5)}`, {
              defaultValue: part.slice(5),
            });
          if (part.startsWith('off='))
            return t(`${ns}.thinkingOff.${part.slice(4)}`, {
              defaultValue: part.slice(4),
            });
          return part.replaceAll(',', ' · ');
        })
        .join(' · ');
    default:
      return value;
  }
};

/** Why a disagreement might be on either side — the queue's 说明 column. */
export const diffNoteText = (note: string, t: TFunction) => {
  switch (note) {
    case 'release_moved':
      return t(`${ns}.note.releaseMoved`, {
        defaultValue: '名字可能已指向新版本，也可能是上游记错了',
      });
    case 'limit_changed':
      return t(`${ns}.note.limitChanged`, {
        defaultValue: '可能是上游更新了，也可能是这边填错了',
      });
    case 'price_changed':
      return t(`${ns}.note.priceChanged`, {
        defaultValue: '可能是官方调价，也可能是限时折扣',
      });
    default:
      return '';
  }
};

/** "272K" for a token threshold. */
export const formatThreshold = (tokens: number) =>
  tokens >= 1_000_000 && tokens % 100_000 === 0
    ? `${tokens / 1_000_000}M`
    : `${Math.round(tokens / 1000)}K`;

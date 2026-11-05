import type { ModelOpsSource } from '../../hooks/models/useModelOpsQuery';
import type { TFunction } from 'i18next';

const ns = 'page.console.modelOps';

/** A source whose every route is still being watched: it carries no traffic
 *  at all yet, so its row shows the watch instead of a live record. */
export const isObservingSource = (source: ModelOpsSource) =>
  Boolean(source.observation && !source.observation.live_routes);

/** The upstream's display name. A type the console has no name for arrives as
 *  its bare number, which reads as a count next to "12 个模型". */
const sourceTypeName = (source: ModelOpsSource, t: TFunction) => {
  const name = String(source.type_name || '').trim();
  if (name && !/^\d+$/.test(name)) return name;
  return t(`${ns}.sources.unknownType`, {
    defaultValue: '未知类型 {{type}}',
    type: name || source.type,
  });
};

/* The adapters 新建来源 offers, named the way its type buttons name them. */
const ADAPTER_LABEL: Record<number, [string, string]> = {
  1: ['openai', 'OpenAI 兼容'],
  8: ['custom', '自定义'],
  14: ['anthropic', 'Anthropic'],
  24: ['gemini', 'Gemini'],
  48: ['xai', 'xAI'],
};

/** What a source speaks, as the source pages label it. */
export const sourceAdapterLabel = (source: ModelOpsSource, t: TFunction) => {
  const known = ADAPTER_LABEL[Number(source.type)];
  return known
    ? t(`page.console.channel.form.create.typeOption.${known[0]}`, {
        defaultValue: known[1],
      })
    : sourceTypeName(source, t);
};

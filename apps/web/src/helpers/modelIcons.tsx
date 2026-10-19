import i18next from 'i18next';
import { stringToColor } from './color';
import type { ComponentType, ReactNode } from 'react';
import { Tag } from '@/components/ui/console-primitives';
import { getProviderIcon } from '@/components/ui/provider-icons';

type LegacyProps = {
  children?: ReactNode;
  [key: string]: unknown;
};

const LegacyTag = Tag as unknown as ComponentType<LegacyProps>;

type ModelLike = {
  model_name: string;
};

export type ModelCategory = {
  filter: (model: ModelLike) => boolean;
  icon: ReactNode | null;
  label: ReactNode;
};

type ModelCategories = Record<string, ModelCategory>;

type Translate = (key: string) => string;

type RenderModelTagOptions = {
  color?: string;
  onClick?: () => void;
  shape?: string;
  size?: string;
  suffixIcon?: ReactNode;
};

// 获取模型分类
export const getModelCategories = (() => {
  let categoriesCache: ModelCategories | null = null;
  let lastLocale: string | null = null;

  return (t: Translate): ModelCategories => {
    const currentLocale = i18next.language;
    if (categoriesCache && lastLocale === currentLocale) {
      return categoriesCache;
    }

    categoriesCache = {
      all: {
        label: t('legacy.i18n.all_models.688facc5f5'),
        icon: null,
        filter: () => true,
      },
      openai: {
        label: 'OpenAI',
        icon: getProviderIcon('OpenAI'),
        filter: (model) =>
          model.model_name.toLowerCase().includes('gpt') ||
          model.model_name.toLowerCase().includes('dall-e') ||
          model.model_name.toLowerCase().includes('whisper') ||
          model.model_name.toLowerCase().includes('tts') ||
          model.model_name.toLowerCase().includes('text-') ||
          model.model_name.toLowerCase().includes('babbage') ||
          model.model_name.toLowerCase().includes('davinci') ||
          model.model_name.toLowerCase().includes('curie') ||
          model.model_name.toLowerCase().includes('ada') ||
          model.model_name.toLowerCase().includes('o1') ||
          model.model_name.toLowerCase().includes('o3') ||
          model.model_name.toLowerCase().includes('o4'),
      },
      anthropic: {
        label: 'Anthropic',
        icon: getProviderIcon('Claude.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('claude'),
      },
      gemini: {
        label: 'Gemini',
        icon: getProviderIcon('Gemini.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('gemini'),
      },
      moonshot: {
        label: 'Moonshot',
        icon: getProviderIcon('Moonshot'),
        filter: (model) => model.model_name.toLowerCase().includes('moonshot'),
      },
      zhipu: {
        label: t('legacy.i18n.zhipu_ai.90fd8f044d'),
        icon: getProviderIcon('Zhipu.Color'),
        filter: (model) =>
          model.model_name.toLowerCase().includes('chatglm') ||
          model.model_name.toLowerCase().includes('glm-'),
      },
      qwen: {
        label: t('legacy.i18n.qwen.b1663a13d6'),
        icon: getProviderIcon('Qwen.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('qwen'),
      },
      deepseek: {
        label: 'DeepSeek',
        icon: getProviderIcon('DeepSeek.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('deepseek'),
      },
      minimax: {
        label: 'MiniMax',
        icon: getProviderIcon('Minimax.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('abab'),
      },
      baidu: {
        label: t('legacy.i18n.ernie_bot.ac34e2863b'),
        icon: getProviderIcon('Wenxin.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('ernie'),
      },
      xunfei: {
        label: t('legacy.i18n.spark_desk.2fe67d4c3e'),
        icon: getProviderIcon('Spark.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('spark'),
      },
      tencent: {
        label: t('legacy.i18n.hunyuan.b68098bb16'),
        icon: getProviderIcon('Hunyuan.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('hunyuan'),
      },
      cohere: {
        label: 'Cohere',
        icon: getProviderIcon('Cohere.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('command'),
      },
      cloudflare: {
        label: 'Cloudflare',
        icon: getProviderIcon('Cloudflare.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('@cf/'),
      },
      ai360: {
        label: t('legacy.i18n.n_360_ai_brain.5e29f93b6a'),
        icon: getProviderIcon('Ai360.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('360'),
      },
      yi: {
        label: t('legacy.i18n.key.f8c4222d0d'),
        icon: getProviderIcon('Yi.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('yi'),
      },
      jina: {
        label: 'Jina',
        icon: getProviderIcon('Jina'),
        filter: (model) => model.model_name.toLowerCase().includes('jina'),
      },
      mistral: {
        label: 'Mistral AI',
        icon: getProviderIcon('Mistral.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('mistral'),
      },
      xai: {
        label: 'xAI',
        icon: getProviderIcon('XAI'),
        filter: (model) => model.model_name.toLowerCase().includes('grok'),
      },
      llama: {
        label: 'Llama',
        icon: getProviderIcon('Ollama'),
        filter: (model) => model.model_name.toLowerCase().includes('llama'),
      },
      seed: {
        label: 'Seed',
        icon: getProviderIcon('Seed'),
        filter: (model) =>
          /^(?:(?:dola|doubao)-)?seed(?:ream)?[-.]/i.test(model.model_name),
      },
      doubao: {
        label: t('legacy.i18n.doubao.0e00b819cb'),
        icon: getProviderIcon('Doubao.Color'),
        filter: (model) => model.model_name.toLowerCase().includes('doubao'),
      },
    };

    lastLocale = currentLocale;
    return categoriesCache;
  };
})();

// 渲染带有模型图标的标签
export function renderModelTag(
  modelName: string,
  options: RenderModelTagOptions = {},
) {
  const {
    color,
    size = 'default',
    shape = 'circle',
    onClick,
    suffixIcon,
  } = options;

  const categories = getModelCategories(i18next.t);
  let icon = null;

  for (const [key, category] of Object.entries(categories)) {
    if (key !== 'all' && category.filter({ model_name: modelName })) {
      icon = category.icon;
      break;
    }
  }

  return (
    <LegacyTag
      color={color || stringToColor(modelName)}
      prefixIcon={icon}
      suffixIcon={suffixIcon}
      size={size}
      shape={shape}
      onClick={onClick}
    >
      {modelName}
    </LegacyTag>
  );
}

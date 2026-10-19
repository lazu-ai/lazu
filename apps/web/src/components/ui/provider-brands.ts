import ai21LabsLogo from '../../brand/assets/providers/ai21-labs.svg';
import ai21LabsLogoSvg from '../../brand/assets/providers/ai21-labs.svg?raw';
import ai360Logo from '../../brand/assets/providers/ai360.svg';
import alibabaCloudLogo from '../../brand/assets/providers/alibaba-cloud.svg';
import awsLogo from '../../brand/assets/providers/amazon-web-services.svg';
import awsLogoSvg from '../../brand/assets/providers/amazon-web-services.svg?raw';
import anthropicLogo from '../../brand/assets/providers/anthropic.svg';
import anthropicLogoSvg from '../../brand/assets/providers/anthropic.svg?raw';
import baichuanLogo from '../../brand/assets/providers/baichuan-ai.svg';
import baiduLogo from '../../brand/assets/providers/baidu.svg';
import blackForestLabsLogo from '../../brand/assets/providers/black-forest-labs.svg';
import blackForestLabsLogoSvg from '../../brand/assets/providers/black-forest-labs.svg?raw';
import bytedanceLogo from '../../brand/assets/providers/bytedance.svg';
import cloudflareLogo from '../../brand/assets/providers/cloudflare.svg';
import cohereLogo from '../../brand/assets/providers/cohere.svg';
import deepseekLogo from '../../brand/assets/providers/deepseek.svg';
import googleLogo from '../../brand/assets/providers/google.svg';
import iflytekLogo from '../../brand/assets/providers/iflytek.svg';
import jinaLogo from '../../brand/assets/providers/jina-ai.svg';
import jinaLogoSvg from '../../brand/assets/providers/jina-ai.svg?raw';
import kuaishouLogo from '../../brand/assets/providers/kuaishou.svg';
import metaLogo from '../../brand/assets/providers/meta.svg';
import microsoftLogo from '../../brand/assets/providers/microsoft.svg';
import minimaxLogo from '../../brand/assets/providers/minimax.svg';
import mistralLogo from '../../brand/assets/providers/mistral-ai.svg';
import moonshotLogo from '../../brand/assets/providers/moonshot-ai.svg';
import moonshotLogoSvg from '../../brand/assets/providers/moonshot-ai.svg?raw';
import nvidiaLogo from '../../brand/assets/providers/nvidia.svg';
import ollamaLogo from '../../brand/assets/providers/ollama.svg';
import ollamaLogoSvg from '../../brand/assets/providers/ollama.svg?raw';
import openaiLogo from '../../brand/assets/providers/openai.svg';
import openaiLogoSvg from '../../brand/assets/providers/openai.svg?raw';
import openrouterLogo from '../../brand/assets/providers/openrouter.svg';
import openrouterLogoSvg from '../../brand/assets/providers/openrouter.svg?raw';
import perplexityLogo from '../../brand/assets/providers/perplexity.svg';
import stabilityLogo from '../../brand/assets/providers/stability-ai.svg';
import stepfunLogo from '../../brand/assets/providers/stepfun.svg';
import tencentLogo from '../../brand/assets/providers/tencent.svg';
import xaiLogo from '../../brand/assets/providers/xai.svg';
import xaiLogoSvg from '../../brand/assets/providers/xai.svg?raw';
import xiaomiLogo from '../../brand/assets/providers/xiaomi.svg';
import xiaomiLogoSvg from '../../brand/assets/providers/xiaomi.svg?raw';
import zeroOneLogo from '../../brand/assets/providers/zero-one-ai.svg';
import zeroOneLogoSvg from '../../brand/assets/providers/zero-one-ai.svg?raw';
import zhipuLogo from '../../brand/assets/providers/zhipu-ai.svg';

export type ProviderBrand = {
  companyName: string;
  id: string;
  logoSrc?: string;
  logoSvg?: string;
  monochrome?: boolean;
};

export type ProviderBrandInput = {
  family?: string;
  id?: string;
  name?: string;
  owned_by?: string;
  provider?: string;
};

type ProviderBrandDefinition = ProviderBrand & {
  matches: RegExp;
};

const PROVIDER_BRANDS: ProviderBrandDefinition[] = [
  {
    id: 'openai',
    companyName: 'OpenAI',
    logoSrc: openaiLogo,
    logoSvg: openaiLogoSvg,
    monochrome: true,
    matches:
      /\bopenai\b|\bgpt(?:[-\s/:]|$)|dall-e|whisper|\bsora\b|\bo[1345](?:[-\s/:]|$)/i,
  },
  {
    id: 'anthropic',
    companyName: 'Anthropic',
    logoSrc: anthropicLogo,
    logoSvg: anthropicLogoSvg,
    monochrome: true,
    matches: /anthropic|claude/i,
  },
  {
    id: 'google',
    companyName: 'Google',
    logoSrc: googleLogo,
    matches: /\bgoogle\b|gemini|gemma|imagen|\bveo(?:[-\s/:]|$)/i,
  },
  {
    id: 'zhipu-ai',
    companyName: 'Zhipu AI',
    logoSrc: zhipuLogo,
    matches: /zhipu|chatglm|\bglm(?:[-\s/:]|$)|\bz\.ai\b/i,
  },
  {
    id: 'alibaba-cloud',
    companyName: 'Alibaba Cloud',
    logoSrc: alibabaCloudLogo,
    matches: /alibaba|aliyun|dashscope|\bqwen|tongyi/i,
  },
  {
    id: 'deepseek',
    companyName: 'DeepSeek',
    logoSrc: deepseekLogo,
    matches: /deepseek/i,
  },
  {
    id: 'moonshot-ai',
    companyName: 'Moonshot AI',
    logoSrc: moonshotLogo,
    logoSvg: moonshotLogoSvg,
    monochrome: true,
    matches: /moonshot|\bkimi(?:[-\s/:]|$)/i,
  },
  {
    id: 'minimax',
    companyName: 'MiniMax',
    logoSrc: minimaxLogo,
    matches: /minimax|\babab(?:[-\s/:]|$)/i,
  },
  {
    id: 'baidu',
    companyName: 'Baidu',
    logoSrc: baiduLogo,
    matches: /baidu|wenxin|ernie/i,
  },
  {
    id: 'zero-one-ai',
    companyName: '01.AI',
    logoSrc: zeroOneLogo,
    logoSvg: zeroOneLogoSvg,
    monochrome: true,
    matches: /zeroone|01\.ai|01-ai|\byi(?:[-\s/:]|$)/i,
  },
  {
    id: 'iflytek',
    companyName: 'iFLYTEK',
    logoSrc: iflytekLogo,
    matches: /iflytek|xunfei|sparkdesk|\bspark(?:[-\s/:]|$)/i,
  },
  {
    id: 'tencent',
    companyName: 'Tencent',
    logoSrc: tencentLogo,
    matches: /tencent|hunyuan/i,
  },
  {
    id: 'cohere',
    companyName: 'Cohere',
    logoSrc: cohereLogo,
    matches: /cohere|\bcommand(?:[-\s/:]|$)/i,
  },
  {
    id: 'cloudflare',
    companyName: 'Cloudflare',
    logoSrc: cloudflareLogo,
    matches: /cloudflare|@cf\//i,
  },
  {
    id: 'ai360',
    companyName: '360 AI',
    logoSrc: ai360Logo,
    matches: /\bai360\b|\b360gpt(?:[_\-\s/:]|$)|\b360(?:[-\s/:]|$)/i,
  },
  {
    id: 'jina-ai',
    companyName: 'Jina AI',
    logoSrc: jinaLogo,
    logoSvg: jinaLogoSvg,
    monochrome: true,
    matches: /\bjina\b/i,
  },
  {
    id: 'mistral-ai',
    companyName: 'Mistral AI',
    logoSrc: mistralLogo,
    matches: /mistral|mixtral|codestral/i,
  },
  {
    id: 'xai',
    companyName: 'xAI',
    logoSrc: xaiLogo,
    logoSvg: xaiLogoSvg,
    monochrome: true,
    matches: /\bxai\b|\bx\.ai\b|\bgrok(?:[-\s/:]|$)/i,
  },
  {
    id: 'meta',
    companyName: 'Meta',
    logoSrc: metaLogo,
    matches: /\bmeta\b|\bllama(?:[-\s/:]|$)/i,
  },
  {
    id: 'seed',
    companyName: 'ByteDance',
    // Seed's official site uses the ByteDance mark for its family identity.
    logoSrc: bytedanceLogo,
    matches: /\b(?:dola[-\s])?(?:doubao[-\s])?seed(?:ream)?(?:[-\s.]|$)/i,
  },
  {
    id: 'bytedance',
    companyName: 'ByteDance',
    logoSrc: bytedanceLogo,
    matches: /bytedance|volcengine|doubao/i,
  },
  {
    id: 'perplexity',
    companyName: 'Perplexity',
    logoSrc: perplexityLogo,
    matches: /perplexity|\bpplx(?:[-\s/:]|$)/i,
  },
  {
    id: 'stability-ai',
    companyName: 'Stability AI',
    logoSrc: stabilityLogo,
    matches: /stability|stable-diffusion|\bsd(?:3|xl)(?:[-\s/:]|$)/i,
  },
  {
    id: 'black-forest-labs',
    companyName: 'Black Forest Labs',
    logoSrc: blackForestLabsLogo,
    logoSvg: blackForestLabsLogoSvg,
    monochrome: true,
    matches: /black forest|\bbfl\b|\bflux(?:[-\s/:.]|$)/i,
  },
  {
    id: 'nvidia',
    companyName: 'NVIDIA',
    logoSrc: nvidiaLogo,
    matches: /nvidia|nemotron/i,
  },
  {
    id: 'microsoft',
    companyName: 'Microsoft',
    logoSrc: microsoftLogo,
    matches: /microsoft|\bphi(?:[-\s/:]|$)|\bmai(?:[-\s/:]|$)/i,
  },
  {
    id: 'amazon',
    companyName: 'Amazon',
    logoSrc: awsLogo,
    logoSvg: awsLogoSvg,
    monochrome: true,
    matches: /\bamazon\b|\baws\b|\bnova(?:[-\s/:]|$)/i,
  },
  {
    id: 'stepfun',
    companyName: 'StepFun',
    logoSrc: stepfunLogo,
    matches: /stepfun|\bstep(?:[-\s/:]|$)/i,
  },
  {
    id: 'baichuan-ai',
    companyName: 'Baichuan AI',
    logoSrc: baichuanLogo,
    matches: /baichuan/i,
  },
  {
    id: 'xiaomi',
    companyName: 'Xiaomi',
    logoSrc: xiaomiLogo,
    logoSvg: xiaomiLogoSvg,
    monochrome: true,
    matches: /xiaomi|\bmimo(?:[-\s/:]|$)/i,
  },
  {
    id: 'kuaishou',
    companyName: 'Kuaishou',
    logoSrc: kuaishouLogo,
    matches: /kuaishou|\bkling(?:[-\s/:]|$)/i,
  },
  {
    id: 'ai21-labs',
    companyName: 'AI21 Labs',
    logoSrc: ai21LabsLogo,
    logoSvg: ai21LabsLogoSvg,
    monochrome: true,
    matches: /\bai21\b|\bjamba(?:[-\s/:]|$)/i,
  },
  {
    id: 'openrouter',
    companyName: 'OpenRouter',
    logoSrc: openrouterLogo,
    logoSvg: openrouterLogoSvg,
    monochrome: true,
    matches: /openrouter/i,
  },
  {
    id: 'ollama',
    companyName: 'Ollama',
    logoSrc: ollamaLogo,
    logoSvg: ollamaLogoSvg,
    monochrome: true,
    matches: /\bollama\b/i,
  },
];

const fallbackCompanyName = (input: ProviderBrandInput) => {
  const candidate = [input.provider, input.owned_by, input.family]
    .find((value) => value && value.trim())
    ?.trim();
  if (!candidate || /^(custom|other|unknown)$/i.test(candidate)) return 'Other';
  return candidate;
};

const fallbackId = (companyName: string) => {
  const slug = companyName
    .normalize('NFKC')
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, '-')
    .replace(/^-|-$/g, '');
  return `custom-${slug || 'other'}`;
};

/** Resolve the company that makes a model, not the gateway/channel that serves
 * it. Model id, name and family therefore come before provider metadata. */
export function resolveProviderBrand(input: ProviderBrandInput): ProviderBrand {
  const known = [
    input.id,
    input.name,
    input.family,
    input.provider,
    input.owned_by,
  ].reduce<ProviderBrandDefinition | undefined>(
    (resolved, value) =>
      resolved ||
      (value
        ? PROVIDER_BRANDS.find((brand) => brand.matches.test(value))
        : undefined),
    undefined,
  );
  if (known) {
    return {
      companyName: known.companyName,
      id: known.id,
      logoSrc: known.logoSrc,
      logoSvg: known.logoSvg,
      monochrome: known.monochrome,
    };
  }
  const companyName = fallbackCompanyName(input);
  return { companyName, id: fallbackId(companyName) };
}

export function resolveProviderBrandDescriptor(raw: unknown): ProviderBrand {
  const descriptor = providerDescriptorName(raw);
  return resolveProviderBrand({ family: descriptor });
}

/** Legacy icon descriptors append dot-separated `key=value` properties. A
 * provider name itself may contain dots (`01.AI`, `X.AI`), so only the first
 * property segment terminates the provider portion. */
export function providerDescriptorName(raw: unknown): string {
  if (typeof raw !== 'string') return '';
  const segments = raw.trim().split('.');
  const propertyIndex = segments.findIndex((segment) => segment.includes('='));
  return (propertyIndex === -1 ? segments : segments.slice(0, propertyIndex))
    .join('.')
    .trim();
}

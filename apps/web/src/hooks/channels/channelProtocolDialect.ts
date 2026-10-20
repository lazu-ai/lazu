export const DEFAULT_PROTOCOL_DIALECT = 'openai_chat_compatible';

export const defaultProtocolDialectForChannelType = (
  channelType: unknown,
): string => {
  switch (Number(channelType)) {
    case 1:
      return 'openai';
    case 14:
      return 'anthropic';
    case 24:
      return 'gemini';
    default:
      return DEFAULT_PROTOCOL_DIALECT;
  }
};

export const inferProtocolDialect = (
  baseURL: unknown,
  channelType: unknown,
): string => {
  const value = String(baseURL || '')
    .trim()
    .toLowerCase();

  let hostname = '';
  let pathname = '';
  try {
    const parsed = new URL(value);
    hostname = parsed.hostname.replace(/\.$/, '');
    pathname = parsed.pathname;
  } catch {
    // Keep malformed and provider-like custom URLs on the conservative
    // compatible dialect instead of matching arbitrary substrings.
  }

  if (
    hostname === 'api.cloudflare.com' &&
    pathname.startsWith('/client/v4/accounts/')
  ) {
    return 'cloudflare_ai_v1';
  }
  if (hostname === 'openrouter.ai' || hostname.endsWith('.openrouter.ai')) {
    return 'openrouter';
  }
  if (hostname === 'api.openai.com') return 'openai';
  if (hostname === 'api.deepseek.com') return 'deepseek';
  if (
    hostname === 'openai.azure.com' ||
    hostname.endsWith('.openai.azure.com') ||
    hostname === 'services.ai.azure.com' ||
    hostname.endsWith('.services.ai.azure.com')
  ) {
    return 'azure_openai';
  }
  if (!value) return defaultProtocolDialectForChannelType(channelType);
  return DEFAULT_PROTOCOL_DIALECT;
};

export const protocolDialectAfterBaseURLChange = (
  baseURL: unknown,
  channelType: unknown,
  currentDialect: unknown,
  manuallySelected: boolean,
): string =>
  manuallySelected
    ? String(currentDialect || DEFAULT_PROTOCOL_DIALECT)
    : inferProtocolDialect(baseURL, channelType);

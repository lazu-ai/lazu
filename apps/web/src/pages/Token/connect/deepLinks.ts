import type { ConnectTool } from './tools';

export type DeepLinkContext = {
  baseURL: string;
  /** Page origin, used as the provider homepage. */
  homepage: string;
  key: string;
  light: string;
  model: string;
  /** Claude Code's `/model opus` and `/model sonnet` slots. CC Switch
   *  carries them for its Claude app only; its other apps take one model. */
  opus?: string;
  sonnet?: string;
};

const toBase64 = (value: string) => {
  const bytes = new TextEncoder().encode(value);
  let binary = '';
  bytes.forEach((byte) => {
    binary += String.fromCharCode(byte);
  });
  return btoa(binary);
};

/** Endpoint each CC Switch app expects: the Anthropic and Gemini SDKs append
 *  their own version path, the OpenAI-shaped clients do not. */
const ccSwitchEndpoint = (tool: ConnectTool, baseURL: string) =>
  tool.ccSwitchApp === 'claude' || tool.ccSwitchApp === 'gemini'
    ? baseURL
    : `${baseURL}/v1`;

/** Returns the URL that opens the tool's own import flow, or '' when the tool
 *  has none. Only custom schemes qualify — they hand the Key to an installed
 *  app rather than putting it on the network. */
export function buildDeepLink(tool: ConnectTool, ctx: DeepLinkContext): string {
  const { baseURL, key } = ctx;
  switch (tool.quick) {
    case 'ccswitch': {
      if (!tool.ccSwitchApp) return '';
      const params = new URLSearchParams({
        resource: 'provider',
        app: tool.ccSwitchApp,
        name: 'Lazu',
        homepage: ctx.homepage,
        endpoint: ccSwitchEndpoint(tool, baseURL),
        apiKey: key,
      });
      if (ctx.model) params.set('model', ctx.model);
      if (tool.ccSwitchApp === 'claude') {
        if (ctx.opus) params.set('opusModel', ctx.opus);
        if (ctx.sonnet) params.set('sonnetModel', ctx.sonnet);
        if (ctx.light) params.set('haikuModel', ctx.light);
        // CC Switch merges this env block under the URL fields, so the two
        // variables third-party gateways need arrive without manual edits.
        params.set(
          'config',
          toBase64(
            JSON.stringify({
              env: {
                CLAUDE_CODE_ATTRIBUTION_HEADER: '0',
                CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS: '1',
              },
            }),
          ),
        );
        params.set('configFormat', 'json');
      }
      return `ccswitch://v1/import?${params.toString()}`;
    }
    case 'cherry':
      return `cherrystudio://providers/api-keys?v=1&data=${encodeURIComponent(
        toBase64(
          JSON.stringify({
            id: 'lazu',
            name: 'Lazu',
            type: 'openai',
            baseUrl: baseURL,
            apiKey: key,
          }),
        ),
      )}`;
    case 'opencat':
      return `opencat://team/join?domain=${encodeURIComponent(baseURL)}&token=${encodeURIComponent(key)}`;
    // LobeChat's import link is an ordinary https URL, so the Key would ride
    // in a query string through the browser's history, the referrer and any
    // proxy log on the way. It gets the manual steps instead.
    default:
      return '';
  }
}

/** Decoded, human-readable form of the link for the code block. */
export function describeDeepLink(link: string): string {
  if (!link) return '';
  try {
    const url = new URL(link);
    const lines = [link];
    const data = url.searchParams.get('data');
    if (data) {
      lines.push('', prettyJSON(atobUtf8(data)));
    }
    const config = url.searchParams.get('config');
    if (config) {
      lines.push('', `config = ${prettyJSON(atobUtf8(config))}`);
    }
    const settings = url.searchParams.get('settings');
    if (settings) {
      lines.push('', prettyJSON(settings));
    }
    return lines.join('\n');
  } catch {
    return link;
  }
}

function prettyJSON(value: string) {
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}

function atobUtf8(value: string) {
  const binary = atob(value);
  const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
  return new TextDecoder().decode(bytes);
}

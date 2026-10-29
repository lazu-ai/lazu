import { extensionI18n } from '../../extensions/i18n';
import { cn } from '../../lib/cn';

/**
 * The client that sent a session or request, as a small ink monogram. Only
 * text — no vendor logos — so a third party's mark never stands in for our
 * judgement of who sent the traffic.
 */
const AGENTS: Record<string, { mark: string; name: string }> = {
  'claude-code': { mark: 'CC', name: 'Claude Code' },
  codex: { mark: 'CX', name: 'Codex' },
  opencode: { mark: 'OC', name: 'OpenCode' },
  'kilo-code': { mark: 'KC', name: 'Kilo Code' },
  'roo-code': { mark: 'RC', name: 'Roo Code' },
  cline: { mark: 'CL', name: 'Cline' },
  crush: { mark: 'CR', name: 'Crush' },
  goose: { mark: 'GS', name: 'Goose' },
  pi: { mark: 'pi', name: 'pi' },
  openclaw: { mark: 'OW', name: 'OpenClaw' },
  'gemini-cli': { mark: 'GM', name: 'Gemini CLI' },
  'qwen-code': { mark: 'QW', name: 'Qwen Code' },
  'kimi-cli': { mark: 'KM', name: 'Kimi CLI' },
  cursor: { mark: 'Cu', name: 'Cursor' },
  zed: { mark: 'ZD', name: 'Zed' },
  aider: { mark: 'AI', name: 'Aider' },
  continue: { mark: 'CT', name: 'Continue' },
  'iflow-cli': { mark: 'iF', name: 'iFlow CLI' },
  'github-copilot': { mark: 'GH', name: 'GitHub Copilot' },
  'hermes-agent': { mark: 'HA', name: 'Hermes Agent' },
  jan: { mark: 'JN', name: 'Jan' },
  'cc-switch': { mark: 'CS', name: 'CC Switch' },
  anythingllm: { mark: 'AL', name: 'AnythingLLM' },
  lobechat: { mark: 'LB', name: 'LobeChat' },
  'cherry-studio': { mark: 'CH', name: 'Cherry Studio' },
  chatbox: { mark: 'CB', name: 'Chatbox' },
  'open-webui': { mark: 'OU', name: 'Open WebUI' },
  sillytavern: { mark: 'ST', name: 'SillyTavern' },
  librechat: { mark: 'LC', name: 'LibreChat' },
  void: { mark: 'VD', name: 'Void' },
  'openai-agents': { mark: 'AG', name: 'OpenAI Agents SDK' },
  mastra: { mark: 'MA', name: 'Mastra' },
  ...Object.assign(
    {},
    ...extensionI18n.map((extension) => extension.clientBadges ?? {}),
  ),
};

/**
 * SDKs, HTTP clients and browsers: transports many programs share, so they
 * get a readable name but never count as a recognised agent.
 */
const LIBRARIES: Record<string, string> = {
  'openai-python': 'OpenAI SDK (Python)',
  'openai-node': 'OpenAI SDK (Node)',
  'openai-go': 'OpenAI SDK (Go)',
  'openai-java': 'OpenAI SDK (Java)',
  'openai-dotnet': 'OpenAI SDK (.NET)',
  'anthropic-python': 'Anthropic SDK (Python)',
  'anthropic-typescript': 'Anthropic SDK (TypeScript)',
  'anthropic-go': 'Anthropic SDK (Go)',
  'anthropic-java': 'Anthropic SDK (Java)',
  'stainless-js': 'OpenAI / Anthropic SDK (bundled)',
  'google-genai': 'Google GenAI SDK',
  'google-genai-python': 'Google GenAI SDK',
  'mistral-python': 'Mistral SDK',
  langchain: 'LangChain',
  litellm: 'LiteLLM',
  'llama-index': 'LlamaIndex',
  'pydantic-ai': 'PydanticAI',
  'semantic-kernel': 'Semantic Kernel',
  'spring-ai': 'Spring AI',
  'ai-sdk': 'Vercel AI SDK',
  curl: 'curl',
  wget: 'wget',
  httpie: 'HTTPie',
  postman: 'Postman',
  insomnia: 'Insomnia',
  axios: 'axios',
  'node-fetch': 'Node fetch',
  bun: 'Bun',
  'python-requests': 'Python requests',
  'python-httpx': 'Python httpx',
  'python-urllib': 'Python urllib',
  okhttp: 'OkHttp',
  aiohttp: 'aiohttp',
  'go-http': 'Go net/http',
  reqwest: 'Rust reqwest',
  'java-http': 'Java HTTP client',
  ktor: 'Ktor',
  electron: 'Electron app',
  'browser-chrome': 'Chrome',
  'browser-firefox': 'Firefox',
  'browser-safari': 'Safari',
};

export function agentName(client: string | undefined | null): string {
  const key = (client || '').trim().toLowerCase();
  return AGENTS[key]?.name || LIBRARIES[key] || client || '—';
}

export function isKnownAgent(client: string | undefined | null): boolean {
  return Boolean(AGENTS[(client || '').trim().toLowerCase()]);
}

type AgentBadgeProps = {
  client?: string | null;
  /** Dashed outline: traffic grouped by Key because it carried no session id. */
  ungrouped?: boolean;
  size?: 'sm' | 'md';
  className?: string;
};

export function AgentBadge({
  client,
  ungrouped,
  size = 'md',
  className,
}: AgentBadgeProps) {
  const key = (client || '').trim().toLowerCase();
  const agent = AGENTS[key];
  const mark = agent?.mark || (key ? key.slice(0, 2) : '?');
  return (
    <span
      className={cn('lz-agent-badge', className)}
      data-size={size}
      data-ghost={ungrouped || !agent || undefined}
      title={agent?.name || client || undefined}
      aria-label={agent?.name || client || undefined}
    >
      {mark}
    </span>
  );
}

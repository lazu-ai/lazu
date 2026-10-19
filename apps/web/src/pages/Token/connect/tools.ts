/** The tools the API Keys page can connect a Key to. One entry per tool; the
 *  product field of a Key stores the tool id so the list can show its badge. */

export type ConnectToolId =
  | 'claude-code'
  | 'codex'
  | 'gemini-cli'
  | 'opencode'
  | 'openclaw'
  | 'cursor'
  | 'cline'
  | 'cherry-studio'
  | 'opencat'
  | 'lobechat'
  | 'chatbox'
  | 'openai-sdk';

export type ConnectGroup = 'agent' | 'editor' | 'chat' | 'sdk';

export type ConnectFormat =
  | 'script'
  | 'powershell'
  | 'env'
  | 'settings'
  | 'toml'
  | 'dotenv'
  | 'json'
  | 'steps'
  | 'link'
  | 'python'
  | 'typescript'
  | 'curl';

/** Which vendor lane a tool's default model belongs to. */
export type ModelFamily = 'claude' | 'openai' | 'gemini';

export type QuickImport = 'ccswitch' | 'cherry' | 'opencat';

/** How a tool holds models beyond its default and light ones.
 *  `aliases`: Claude Code's `/model opus` and `/model sonnet` slots, one
 *  model each. `list`: the provider's own model list (OpenCode's models map,
 *  OpenClaw's models array, the model catalog behind Codex's /model), any
 *  number, switchable inside the app. */
export type ExtraModels = 'aliases' | 'list';

export type ConnectTool = {
  ccSwitchApp?: 'claude' | 'codex' | 'gemini' | 'opencode' | 'openclaw';
  docsUrl: string;
  extraModels?: ExtraModels;
  formats: ConnectFormat[];
  group: ConnectGroup;
  id: ConnectToolId;
  /** The tool reads a second, cheaper model (Claude Code's Haiku slot,
   *  OpenCode's small_model). */
  lightSlot: boolean;
  mono: string;
  name: string;
  quick?: QuickImport;
  /** The tool needs a model id to work at all. */
  requiresModel: boolean;
  /** The tool receives a model id from us. Import links for chat apps do
   *  not; those apps fetch the model list themselves. */
  usesModel: boolean;
  /** Setup script name under /setup/, when the tool has one. */
  script?: string;
};

export const CONNECT_TOOLS: ConnectTool[] = [
  {
    ccSwitchApp: 'claude',
    docsUrl: 'https://code.claude.com/docs/en/llm-gateway-connect',
    extraModels: 'aliases',
    formats: ['script', 'powershell', 'env', 'settings'],
    group: 'agent',
    id: 'claude-code',
    lightSlot: true,
    mono: 'CC',
    name: 'Claude Code',
    quick: 'ccswitch',
    usesModel: true,
    requiresModel: false,
    script: 'claude-code',
  },
  {
    ccSwitchApp: 'codex',
    docsUrl: 'https://github.com/openai/codex/blob/main/docs/config.md',
    extraModels: 'list',
    formats: ['script', 'powershell', 'toml'],
    group: 'agent',
    id: 'codex',
    lightSlot: false,
    mono: 'CX',
    name: 'Codex CLI',
    quick: 'ccswitch',
    usesModel: true,
    requiresModel: true,
    script: 'codex',
  },
  {
    ccSwitchApp: 'gemini',
    docsUrl: 'https://github.com/google-gemini/gemini-cli',
    formats: ['script', 'powershell', 'dotenv'],
    group: 'agent',
    id: 'gemini-cli',
    lightSlot: false,
    mono: 'GC',
    name: 'Gemini CLI',
    quick: 'ccswitch',
    usesModel: true,
    requiresModel: false,
    script: 'gemini-cli',
  },
  {
    ccSwitchApp: 'opencode',
    docsUrl: 'https://opencode.ai/docs/providers/',
    extraModels: 'list',
    formats: ['script', 'powershell', 'json'],
    group: 'agent',
    id: 'opencode',
    lightSlot: true,
    mono: 'OC',
    name: 'OpenCode',
    quick: 'ccswitch',
    usesModel: true,
    requiresModel: false,
    script: 'opencode',
  },
  {
    ccSwitchApp: 'openclaw',
    docsUrl: 'https://docs.openclaw.ai/gateway/config-tools/custom-providers',
    extraModels: 'list',
    formats: ['script', 'powershell', 'json'],
    group: 'agent',
    id: 'openclaw',
    lightSlot: false,
    mono: 'OW',
    name: 'OpenClaw',
    quick: 'ccswitch',
    usesModel: true,
    requiresModel: true,
    script: 'openclaw',
  },
  {
    docsUrl: 'https://docs.cursor.com/settings/api-keys',
    formats: ['steps'],
    group: 'editor',
    id: 'cursor',
    lightSlot: false,
    mono: 'Cu',
    name: 'Cursor',
    usesModel: true,
    requiresModel: true,
  },
  {
    docsUrl: 'https://docs.cline.bot/provider-config/openai-compatible',
    formats: ['steps'],
    group: 'editor',
    id: 'cline',
    lightSlot: false,
    mono: 'Cl',
    name: 'Cline',
    usesModel: true,
    requiresModel: true,
  },
  {
    docsUrl:
      'https://docs.cherry-ai.com/docs/en-us/pre-basic/providers/zi-ding-yi-fu-wu-shang',
    formats: ['link'],
    group: 'chat',
    id: 'cherry-studio',
    lightSlot: false,
    mono: 'CS',
    name: 'Cherry Studio',
    quick: 'cherry',
    usesModel: false,
    requiresModel: false,
  },
  {
    docsUrl: 'https://opencat.app',
    formats: ['link'],
    group: 'chat',
    id: 'opencat',
    lightSlot: false,
    mono: 'OT',
    name: 'OpenCat',
    quick: 'opencat',
    usesModel: false,
    requiresModel: false,
  },
  {
    docsUrl:
      'https://lobehub.com/docs/self-hosting/advanced/settings-url-share',
    formats: ['steps'],
    group: 'chat',
    id: 'lobechat',
    lightSlot: false,
    mono: 'LC',
    name: 'LobeChat',
    usesModel: false,
    requiresModel: false,
  },
  {
    docsUrl: 'https://chatboxai.app',
    formats: ['steps'],
    group: 'chat',
    id: 'chatbox',
    lightSlot: false,
    mono: 'CB',
    name: 'Chatbox',
    usesModel: true,
    requiresModel: true,
  },
  {
    docsUrl: '',
    formats: ['python', 'typescript', 'curl'],
    group: 'sdk',
    id: 'openai-sdk',
    lightSlot: false,
    mono: 'SDK',
    name: 'OpenAI SDK',
    usesModel: true,
    requiresModel: true,
  },
];

export const CONNECT_GROUPS: ConnectGroup[] = [
  'agent',
  'editor',
  'chat',
  'sdk',
];

export const findConnectTool = (value: unknown): ConnectTool | undefined => {
  const id = String(value ?? '')
    .trim()
    .toLowerCase();
  return CONNECT_TOOLS.find((tool) => tool.id === id);
};

export const DEFAULT_CONNECT_TOOL = CONNECT_TOOLS[0];

export function modelFamily(model: string): ModelFamily | null {
  const value = model.toLowerCase();
  if (value.includes('claude')) return 'claude';
  if (value.includes('gemini')) return 'gemini';
  if (/^(gpt|o\d|chatgpt|codex)/.test(value)) return 'openai';
  return null;
}

/** macOS and Linux get the sh installer, Windows the PowerShell one. */
export const prefersPowerShell = (userAgent: string) =>
  /windows/i.test(userAgent);

export const defaultFormat = (tool: ConnectTool, userAgent: string) => {
  if (tool.formats.includes('powershell') && prefersPowerShell(userAgent)) {
    return 'powershell';
  }
  return tool.formats[0];
};

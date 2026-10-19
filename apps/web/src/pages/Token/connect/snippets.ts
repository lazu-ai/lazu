import type { ConnectFormat, ConnectTool } from './tools';
import type { TFunction } from 'i18next';

export type SnippetContext = {
  /** Lazu origin without a trailing slash and without /v1. */
  baseURL: string;
  /** Models a list tool imports beside the default and light ones. */
  extras?: string[];
  key: string;
  light: string;
  model: string;
  /** Claude Code's `/model opus` slot. */
  opus?: string;
  /** Claude Code's `/model sonnet` slot. */
  sonnet?: string;
};

/** The models a list tool writes into its provider: default first, then the
 *  light one, then the extras, each once. A tool without a list gets the
 *  default and light ones only, whatever the context carries. */
export const providerModels = (
  tool: ConnectTool,
  ctx: Pick<SnippetContext, 'extras' | 'light' | 'model'>,
): string[] => {
  const ids = [ctx.model, tool.lightSlot ? ctx.light : ''];
  if (tool.extraModels === 'list') ids.push(...(ctx.extras || []));
  return [...new Set(ids.filter(Boolean))];
};

/** The alias slots Claude Code gets, empty for every other tool. */
const aliasSlots = (tool: ConnectTool, ctx: SnippetContext) =>
  tool.extraModels === 'aliases'
    ? { opus: ctx.opus || '', sonnet: ctx.sonnet || '' }
    : { opus: '', sonnet: '' };

const MODEL_PLACEHOLDER = 'MODEL_ID';

/** Stands in for the Key wherever the snippet is a command a shell would
 *  record in its history. The dialog copies the real value on its own
 *  button, so it never has to travel through a command line. */
const KEY_PLACEHOLDER = 'YOUR_LAZU_KEY';

/** True when the copied block is something the user runs rather than a file
 *  they paste into — those blocks get the placeholder, not the Key.
 *
 *  The distinction is the shell's history file, so the list is by format and
 *  exhaustive: every format is either a command line (these) or something the
 *  user pastes into an editor or a settings window (all the rest — settings,
 *  toml, dotenv, json, steps, link, python, typescript). A new format has to
 *  be classified here before it can carry a Key. */
const RUNNABLE_FORMATS: ConnectFormat[] = [
  'script',
  'powershell',
  'env',
  'curl',
  // Source samples are pasted into repositories, where a literal Key gets
  // committed. They read it from the environment instead.
  'python',
  'typescript',
];

export const isShellCommand = (format: ConnectFormat) =>
  RUNNABLE_FORMATS.includes(format);

/** Formats that show a placeholder where the Key goes, so the dialog knows to
 *  offer its own copy-the-Key button. */
export const snippetHidesKey = (tool: ConnectTool, format: ConnectFormat) =>
  isShellCommand(format) || (tool.id === 'codex' && format === 'toml');

const shellWords = (words: Array<string | false | undefined>) =>
  words.filter(Boolean).join(' ');

/** Single-quote for POSIX shells. Model ids come from the catalog, but they
 *  travel through a command line the user pastes, so nothing goes in bare. */
const sh = (value: string) => `'${value.replace(/'/g, `'\\''`)}'`;

/** Single-quote for PowerShell, where the escape for a quote is doubling it. */
const ps = (value: string) => `'${value.replace(/'/g, "''")}'`;

/** The right-hand side of a .env assignment: a plain value bare, anything
 *  else double-quoted and escaped the way dotenv readers unwrap it. */
const dotenv = (value: string) =>
  /^[A-Za-z0-9._:/@-]*$/.test(value) ? value : JSON.stringify(value);

export const setupScriptURL = (
  baseURL: string,
  tool: ConnectTool,
  shell: 'sh' | 'ps1',
) => `${baseURL}/setup/${tool.script}${shell === 'ps1' ? '.ps1' : ''}`;

/** The text the code block shows (and copies) for one tool and format. */
export function buildSnippet(
  tool: ConnectTool,
  format: ConnectFormat,
  ctx: SnippetContext,
  t: TFunction,
): string {
  const { baseURL, light } = ctx;
  // Anything the user pastes into a terminal gets a placeholder; a config
  // file they edit gets the value itself.
  const key = snippetHidesKey(tool, format) ? KEY_PLACEHOLDER : ctx.key;
  const model = ctx.model || MODEL_PLACEHOLDER;
  const c = (id: string, values?: Record<string, string>) =>
    t(`page.console.token.connect.snippet.${id}`, values);

  // Claude Code calls its light model the Haiku slot, OpenCode the small
  // model; the scripts accept all three spellings.
  const lightFlag = tool.id === 'opencode' ? 'small' : 'haiku';
  const { opus, sonnet } = aliasSlots(tool, ctx);
  // The extras alone, for the scripts: the default model, and the light one
  // where the tool has a slot for it, already travel on their own flags.
  const also = providerModels(tool, ctx).filter(
    (id) => id !== ctx.model && !(tool.lightSlot && id === light),
  );

  // Nothing in these two commands carries the Key — not a flag, not an
  // `export`: a pasted command line is visible in `ps` and is written to the
  // shell's history file. The script asks for the Key on the terminal instead,
  // and the dialog offers a separate button to copy it.
  if (format === 'script' && tool.script) {
    const modelFlags = shellWords([
      ctx.model && `--model ${sh(ctx.model)}`,
      opus && `--opus ${sh(opus)}`,
      sonnet && `--sonnet ${sh(sonnet)}`,
      tool.lightSlot && light && `--${lightFlag} ${sh(light)}`,
      ...also.map((id) => `--also ${sh(id)}`),
    ]);
    return [
      `# ${c('scriptComment', { tool: tool.name })}`,
      modelFlags
        ? `curl -fsSL ${sh(setupScriptURL(baseURL, tool, 'sh'))} | sh -s -- ${modelFlags}`
        : `curl -fsSL ${sh(setupScriptURL(baseURL, tool, 'sh'))} | sh`,
    ]
      .filter(Boolean)
      .join('\n');
  }

  if (format === 'powershell' && tool.script) {
    const capital = lightFlag === 'small' ? 'Small' : 'Haiku';
    const modelFlags = shellWords([
      ctx.model && `-Model ${ps(ctx.model)}`,
      opus && `-Opus ${ps(opus)}`,
      sonnet && `-Sonnet ${ps(sonnet)}`,
      tool.lightSlot && light && `-${capital} ${ps(light)}`,
      also.length > 0 && `-Also ${also.map(ps).join(',')}`,
    ]);
    const invoke = `& ([scriptblock]::Create((irm ${ps(setupScriptURL(baseURL, tool, 'ps1'))})))`;
    return [
      `# ${c('scriptComment', { tool: tool.name })}`,
      modelFlags ? `${invoke} ${modelFlags}` : invoke,
    ]
      .filter(Boolean)
      .join('\n');
  }

  switch (tool.id) {
    case 'claude-code': {
      const env: Array<[string, string]> = [
        ['ANTHROPIC_BASE_URL', baseURL],
        ['ANTHROPIC_AUTH_TOKEN', key],
      ];
      if (ctx.model) env.push(['ANTHROPIC_MODEL', ctx.model]);
      // The three aliases behind /model opus, sonnet and haiku, so the agent
      // can switch between them without editing this file.
      if (opus) env.push(['ANTHROPIC_DEFAULT_OPUS_MODEL', opus]);
      if (sonnet) env.push(['ANTHROPIC_DEFAULT_SONNET_MODEL', sonnet]);
      if (light) env.push(['ANTHROPIC_DEFAULT_HAIKU_MODEL', light]);
      env.push(['CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS', '1']);
      env.push(['CLAUDE_CODE_ATTRIBUTION_HEADER', '0']);
      if (format === 'settings') {
        return [
          '// ~/.claude/settings.json',
          '{',
          '  "env": {',
          env
            .map(
              ([name, value], index) =>
                `    ${JSON.stringify(name)}: ${JSON.stringify(value)}${index < env.length - 1 ? ',' : ''}`,
            )
            .join('\n'),
          '  }',
          '}',
        ].join('\n');
      }
      return [
        `# ${c('shellRc')}`,
        ...env.map(([name, value]) => `export ${name}=${sh(value)}`),
      ].join('\n');
    }
    case 'codex': {
      // Beyond the default model, Codex lists models from a catalog file whose
      // schema (and base prompt) follows the installed Codex version, so the
      // setup script clones it from `codex debug models --bundled`; this tab
      // names the models and points there rather than pasting a file that a
      // Codex update could refuse to parse.
      const extras = providerModels(tool, ctx).filter((id) => id !== model);
      return [
        '# ~/.codex/config.toml',
        'model_provider = "lazu"',
        `model = ${JSON.stringify(model)}`,
        '',
        '[model_providers.lazu]',
        'name = "Lazu"',
        `base_url = ${JSON.stringify(`${baseURL}/v1`)}`,
        'env_key = "LAZU_API_KEY"',
        'wire_api = "responses"',
        '',
        `# ${c('codexEnv')}`,
        `# LAZU_API_KEY: ${key}`,
        extras.length > 0 && '',
        extras.length > 0 &&
          `# ${c('codexCatalog', { models: extras.join(', ') })}`,
      ]
        .filter((line) => line !== false)
        .join('\n');
    }
    case 'gemini-cli':
      // A dotenv line ends at a # or a space, so a catalog id carrying either
      // would silently become a different model — or a second assignment.
      return [
        `# ~/.gemini/.env — ${c('geminiEnv')}`,
        `GEMINI_API_KEY=${dotenv(key)}`,
        `GOOGLE_GEMINI_BASE_URL=${dotenv(baseURL)}`,
        `GEMINI_MODEL=${dotenv(model)}`,
        '',
        `# ~/.gemini/settings.json — ${c('geminiAuth')}`,
        '{ "security": { "auth": { "selectedType": "gemini-api-key" } } }',
      ].join('\n');
    case 'opencode': {
      const models = providerModels(tool, ctx)
        .map((id) => `${JSON.stringify(id)}: {}`)
        .join(', ');
      return [
        '// ~/.config/opencode/opencode.json',
        '{',
        '  "$schema": "https://opencode.ai/config.json",',
        '  "provider": {',
        '    "lazu": {',
        '      "npm": "@ai-sdk/openai-compatible",',
        '      "name": "Lazu",',
        `      "options": { "baseURL": ${JSON.stringify(`${baseURL}/v1`)}, "apiKey": ${JSON.stringify(key)} },`,
        `      "models": { ${models || `"${MODEL_PLACEHOLDER}": {}`} }`,
        '    }',
        '  },',
        light
          ? `  "model": ${JSON.stringify(`lazu/${model}`)},\n  "small_model": ${JSON.stringify(`lazu/${light}`)}`
          : `  "model": ${JSON.stringify(`lazu/${model}`)}`,
        '}',
      ].join('\n');
    }
    case 'openclaw': {
      const list = providerModels(tool, ctx);
      const entries = (list.length ? list : [model]).map(
        (id) =>
          `{ "id": ${JSON.stringify(id)}, "name": ${JSON.stringify(id)} }`,
      );
      return [
        '// ~/.openclaw/openclaw.json',
        '{',
        '  "models": { "providers": { "lazu": {',
        `    "baseUrl": ${JSON.stringify(`${baseURL}/v1`)},`,
        `    "apiKey": ${JSON.stringify(key)},`,
        '    "api": "openai-completions",',
        entries.length === 1
          ? `    "models": [${entries[0]}]`
          : `    "models": [\n${entries.map((entry) => `      ${entry}`).join(',\n')}\n    ]`,
        '  } } },',
        `  "agents": { "defaults": { "model": ${JSON.stringify(`lazu/${model}`)} } }`,
        '}',
      ].join('\n');
    }
    case 'cursor':
      return [
        `# ${c('cursorPath')}`,
        `1. OpenAI API Key      ${key}`,
        `2. Override base URL   ${baseURL}/v1`,
        `3. ${c('cursorModel')}   ${model}`,
        `4. ${c('cursorVerify')}`,
      ].join('\n');
    case 'cline':
      return [
        `# ${c('clinePath')}`,
        `Base URL   ${baseURL}/v1`,
        `API Key    ${key}`,
        `Model ID   ${model}`,
      ].join('\n');
    case 'lobechat':
      return [
        `# ${c('lobechatPath')}`,
        `API Key    ${key}`,
        `API Proxy  ${baseURL}/v1`,
        `# ${c('lobechatNote')}`,
      ].join('\n');
    case 'chatbox':
      return [
        `# ${c('chatboxPath')}`,
        `API Host   ${baseURL}`,
        `API Key    ${key}`,
        `${c('chatboxModel')}   ${model}`,
      ].join('\n');
    case 'openai-sdk':
      // Model ids are catalog data, not code: a channel may enable an id
      // holding a quote, and these three blocks are pasted into a shell, a
      // JS file and a Python file respectively. Each value goes through its
      // own serializer instead of being dropped between literal quotes.
      if (format === 'typescript') {
        return [
          `// ${c('sdkEnv')}`,
          "import OpenAI from 'openai';",
          '',
          'const client = new OpenAI({',
          '  apiKey: process.env.LAZU_API_KEY,',
          `  baseURL: ${JSON.stringify(`${baseURL}/v1`)},`,
          '});',
          'const response = await client.chat.completions.create({',
          `  model: ${JSON.stringify(model)},`,
          "  messages: [{ role: 'user', content: 'Hello' }],",
          '});',
        ].join('\n');
      }
      if (format === 'curl') {
        // Written out rather than JSON.stringify'd whole, so the reader sees
        // model first and the usual spacing; the one value that comes from
        // the catalog is still escaped as a JSON string.
        const body = `{"model": ${JSON.stringify(model)}, "messages": [{"role": "user", "content": "Hello"}]}`;
        return [
          `curl ${sh(`${baseURL}/v1/chat/completions`)} \\`,
          `  -H ${sh(`Authorization: Bearer ${key}`)} \\`,
          `  -H 'Content-Type: application/json' \\`,
          `  -d ${sh(body)}`,
        ].join('\n');
      }
      return [
        `# ${c('sdkEnv')}`,
        'import os',
        'from openai import OpenAI',
        '',
        'client = OpenAI(',
        '    api_key=os.environ["LAZU_API_KEY"],',
        `    base_url=${JSON.stringify(`${baseURL}/v1`)},`,
        ')',
        'response = client.chat.completions.create(',
        `    model=${JSON.stringify(model)},`,
        '    messages=[{"role": "user", "content": "Hello"}],',
        ')',
      ].join('\n');
    default:
      return '';
  }
}

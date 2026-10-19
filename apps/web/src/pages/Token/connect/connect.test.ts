import { spawnSync } from 'node:child_process';
import { describe, expect, it } from 'vitest';
import { buildDeepLink, describeDeepLink } from './deepLinks';
import { isLegacyKey, shortKeyLabel } from '../tokenKeyLabel';
import { isLoopbackOrigin, isUsableOrigin } from './baseURL';
import { buildSnippet, providerModels, snippetHidesKey } from './snippets';
import {
  CONNECT_TOOLS,
  defaultFormat,
  findConnectTool,
  modelFamily,
} from './tools';
import type { TFunction } from 'i18next';

const t = ((key: string) => key) as unknown as TFunction;
const tool = (id: string) => {
  const found = findConnectTool(id);
  if (!found) throw new Error(`missing tool ${id}`);
  return found;
};
const ctx = {
  baseURL: 'https://api.lazu.ai',
  key: 'sk-lazu-abc123',
  light: 'deepseek-v4-flash',
  model: 'claude-sonnet-5',
};

describe('connect tool catalog', () => {
  it('has unique ids and a format for every tool', () => {
    const ids = CONNECT_TOOLS.map((item) => item.id);
    expect(new Set(ids).size).toBe(ids.length);
    CONNECT_TOOLS.forEach((item) =>
      expect(item.formats.length).toBeGreaterThan(0),
    );
  });

  it('only offers script formats to tools that have a setup script', () => {
    CONNECT_TOOLS.forEach((item) => {
      const scripted = item.formats.includes('script');
      expect(Boolean(item.script)).toBe(scripted);
    });
  });

  it('resolves a stored product value regardless of case', () => {
    expect(findConnectTool(' Claude-Code ')?.name).toBe('Claude Code');
    expect(findConnectTool('search-api')).toBeUndefined();
  });

  it('maps models to vendor families', () => {
    expect(modelFamily('claude-opus-5')).toBe('claude');
    expect(modelFamily('gpt-5.2')).toBe('openai');
    expect(modelFamily('o3-mini')).toBe('openai');
    expect(modelFamily('gemini-2.5-pro')).toBe('gemini');
    expect(modelFamily('deepseek-v4-flash')).toBeNull();
  });

  it('defaults Windows visitors to PowerShell', () => {
    const windows = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64)';
    const mac = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)';
    expect(defaultFormat(tool('claude-code'), windows)).toBe('powershell');
    expect(defaultFormat(tool('claude-code'), mac)).toBe('script');
    expect(defaultFormat(tool('cursor'), windows)).toBe('steps');
  });
});

describe('connect snippets', () => {
  it('builds the sh installer with the light slot only where the tool has one', () => {
    const claude = buildSnippet(tool('claude-code'), 'script', ctx, t);
    expect(claude.split('\n')).toEqual([
      '# page.console.token.connect.snippet.scriptComment',
      "curl -fsSL 'https://api.lazu.ai/setup/claude-code' | sh -s -- --model 'claude-sonnet-5' --haiku 'deepseek-v4-flash'",
    ]);
    const codex = buildSnippet(
      tool('codex'),
      'script',
      { ...ctx, model: 'gpt-5.2' },
      t,
    );
    expect(codex).toContain("/setup/codex' | sh");
    expect(codex).not.toContain('--haiku');
    const opencode = buildSnippet(tool('opencode'), 'script', ctx, t);
    expect(opencode).toContain("--small 'deepseek-v4-flash'");
  });

  it('keeps the Key out of copied commands and quotes model ids', () => {
    const sh = buildSnippet(
      tool('claude-code'),
      'script',
      { ...ctx, light: '', model: 'weird; rm -rf ~' },
      t,
    );
    // Neither a flag nor an export: a pasted command line reaches `ps` and
    // the shell's history file, so the script asks for the Key itself.
    expect(sh).not.toContain(ctx.key);
    expect(sh).toContain("--model 'weird; rm -rf ~'");
    const pwsh = buildSnippet(
      tool('claude-code'),
      'powershell',
      { ...ctx, light: '', model: "it's" },
      t,
    );
    expect(pwsh).not.toContain(ctx.key);
    expect(pwsh).toContain("-Model 'it''s'");
  });

  it('builds the PowerShell installer', () => {
    const snippet = buildSnippet(
      tool('gemini-cli'),
      'powershell',
      { ...ctx, model: 'gemini-2.5-pro' },
      t,
    );
    expect(snippet).toContain("irm 'https://api.lazu.ai/setup/gemini-cli.ps1'");
    expect(snippet).toContain("-Model 'gemini-2.5-pro'");
  });

  it('writes the gateway variables into Claude Code settings', () => {
    const settings = buildSnippet(tool('claude-code'), 'settings', ctx, t);
    const json = JSON.parse(settings.split('\n').slice(1).join('\n'));
    expect(json.env).toEqual({
      ANTHROPIC_AUTH_TOKEN: 'sk-lazu-abc123',
      ANTHROPIC_BASE_URL: 'https://api.lazu.ai',
      ANTHROPIC_DEFAULT_HAIKU_MODEL: 'deepseek-v4-flash',
      ANTHROPIC_MODEL: 'claude-sonnet-5',
      CLAUDE_CODE_ATTRIBUTION_HEADER: '0',
      CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS: '1',
    });
  });

  it('points Codex at the Responses API under /v1', () => {
    const toml = buildSnippet(
      tool('codex'),
      'toml',
      { ...ctx, model: 'gpt-5.2' },
      t,
    );
    expect(toml).toContain('base_url = "https://api.lazu.ai/v1"');
    expect(toml).toContain('wire_api = "responses"');
    expect(toml).toContain('model = "gpt-5.2"');
  });

  it('keeps a readable placeholder when no model is known', () => {
    const snippet = buildSnippet(
      tool('openai-sdk'),
      'python',
      { ...ctx, model: '' },
      t,
    );
    expect(snippet).toContain('model="MODEL_ID"');
  });
});

describe('where the Key may appear', () => {
  it('never puts the Key in a block the user runs', () => {
    CONNECT_TOOLS.forEach((item) => {
      item.formats.forEach((format) => {
        const snippet = buildSnippet(item, format, ctx, t);
        if (snippetHidesKey(item, format)) {
          expect(snippet).not.toContain(ctx.key);
        }
      });
    });
  });

  it('never ships a runnable line holding the Key, whatever the format', () => {
    // This one does not read the format list: it looks at what was generated
    // and asks whether a shell would run it. A new format that forgets to
    // declare itself runnable fails here rather than in somebody's history
    // file.
    // A command spans its continuation lines, so one command line makes the
    // whole block runnable — the Key must not be anywhere in it.
    const startsACommand = (line: string) =>
      /^\s*(curl|export|irm|iwr|sh|bash|\$env:|& )/.test(line) ||
      / \| (sh|bash)\b/.test(line);
    CONNECT_TOOLS.forEach((item) => {
      item.formats.forEach((format) => {
        const snippet = buildSnippet(item, format, ctx, t);
        const runnable = snippet
          .split('\n')
          .filter((line) => !line.trimStart().startsWith('#'))
          .some(startsACommand);
        if (!runnable) return;
        expect(`${item.id}/${format}:\n${snippet}`).not.toContain(ctx.key);
      });
    });
  });

  it('names exactly which blocks may carry the Key', () => {
    // Everything else is either run in a shell or committed to a repository.
    // This list is the whole permission: adding a format, or reclassifying
    // one, has to come through here.
    const carrying: string[] = [];
    CONNECT_TOOLS.forEach((item) =>
      item.formats.forEach((format) => {
        if (buildSnippet(item, format, ctx, t).includes(ctx.key)) {
          carrying.push(`${item.id}/${format}`);
        }
      }),
    );
    expect(carrying.sort()).toEqual(
      [
        // Config files the user pastes into an editor.
        'claude-code/settings',
        'gemini-cli/dotenv',
        'openclaw/json',
        'opencode/json',
        // Values typed into a settings window, one field at a time.
        'chatbox/steps',
        'cline/steps',
        'cursor/steps',
        'lobechat/steps',
      ].sort(),
    );
  });

  it('still fills in config files the user edits', () => {
    // A settings.json or a .env is pasted into an editor, not a shell, so it
    // carries the value and saves the user a round trip.
    expect(buildSnippet(tool('claude-code'), 'settings', ctx, t)).toContain(
      ctx.key,
    );
    expect(buildSnippet(tool('gemini-cli'), 'dotenv', ctx, t)).toContain(
      ctx.key,
    );
    expect(buildSnippet(tool('opencode'), 'json', ctx, t)).toContain(ctx.key);
  });

  it('keeps manual environment setup free of real keys', () => {
    // Manual fallbacks never embed the credential in a copied command.
    expect(snippetHidesKey(tool('claude-code'), 'env')).toBe(true);
    expect(snippetHidesKey(tool('codex'), 'toml')).toBe(true);
    const env = buildSnippet(tool('claude-code'), 'env', ctx, t);
    expect(env).toContain("export ANTHROPIC_AUTH_TOKEN='YOUR_LAZU_KEY'");
    expect(buildSnippet(tool('codex'), 'toml', ctx, t)).toContain(
      '# LAZU_API_KEY: YOUR_LAZU_KEY',
    );
  });
});

describe('hostile catalog values', () => {
  // A channel may enable any model id under 255 bytes, and /api/user/models
  // hands it back untouched. None of these blocks may treat it as code.
  const nasty = `x'; echo BROKEN; '`;

  /** What a real shell makes of the arguments we generated: one line per
   *  word, and nothing executed beyond the printf. */
  const shellWords = (args: string) => {
    const run = spawnSync('sh', ['-c', `printf '%s\\n' ${args}`], {
      encoding: 'utf8',
    });
    expect(run.status).toBe(0);
    return run.stdout.split('\n').filter(Boolean);
  };

  it('passes a hostile model id to the installer as one word', () => {
    const snippet = buildSnippet(
      tool('claude-code'),
      'script',
      { ...ctx, light: nasty, model: nasty },
      t,
    );
    const args = snippet.split('sh -s -- ')[1];
    expect(shellWords(args)).toEqual(['--model', nasty, '--haiku', nasty]);
  });

  it('passes a hostile model id to curl inside one JSON argument', () => {
    const snippet = buildSnippet(
      tool('openai-sdk'),
      'curl',
      { ...ctx, model: nasty },
      t,
    );
    const body = snippet.split('  -d ')[1];
    const words = shellWords(body);
    expect(words).toHaveLength(1);
    expect(JSON.parse(words[0]).model).toBe(nasty);
  });

  it('keeps a hostile model id inside one dotenv assignment', () => {
    const block = buildSnippet(
      tool('gemini-cli'),
      'dotenv',
      { ...ctx, model: 'x y #z' },
      t,
    );
    const line = block
      .split('\n')
      .find((item) => item.startsWith('GEMINI_MODEL=')) as string;
    expect(JSON.parse(line.replace('GEMINI_MODEL=', ''))).toBe('x y #z');
    // The plain case stays readable, without quotes.
    expect(buildSnippet(tool('gemini-cli'), 'dotenv', ctx, t)).toContain(
      'GEMINI_MODEL=claude-sonnet-5',
    );
  });

  it('keeps the JSON config blocks parseable', () => {
    const settings = buildSnippet(
      tool('claude-code'),
      'settings',
      { ...ctx, model: nasty },
      t,
    );
    expect(() =>
      JSON.parse(settings.split('\n').slice(1).join('\n')),
    ).not.toThrow();
    const opencode = buildSnippet(
      tool('opencode'),
      'json',
      { ...ctx, light: '', model: nasty },
      t,
    );
    expect(() =>
      JSON.parse(opencode.split('\n').slice(1).join('\n')),
    ).not.toThrow();
  });
});

describe('connect deep links', () => {
  const link = (id: string, override = {}) =>
    buildDeepLink(tool(id), {
      ...ctx,
      homepage: 'https://lazu.ai',
      ...override,
    });

  it('imports Claude Code into CC Switch with the gateway env block', () => {
    const url = new URL(link('claude-code'));
    expect(url.protocol).toBe('ccswitch:');
    expect(url.searchParams.get('resource')).toBe('provider');
    expect(url.searchParams.get('app')).toBe('claude');
    expect(url.searchParams.get('endpoint')).toBe('https://api.lazu.ai');
    expect(url.searchParams.get('apiKey')).toBe('sk-lazu-abc123');
    expect(url.searchParams.get('haikuModel')).toBe('deepseek-v4-flash');
    expect(JSON.parse(atob(url.searchParams.get('config') || ''))).toEqual({
      env: {
        CLAUDE_CODE_ATTRIBUTION_HEADER: '0',
        CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS: '1',
      },
    });
  });

  it('gives OpenAI-shaped CC Switch apps the /v1 endpoint', () => {
    const url = new URL(link('codex', { model: 'gpt-5.2' }));
    expect(url.searchParams.get('app')).toBe('codex');
    expect(url.searchParams.get('endpoint')).toBe('https://api.lazu.ai/v1');
    expect(url.searchParams.get('config')).toBeNull();
  });

  it('encodes the Cherry Studio provider as base64 JSON', () => {
    const url = new URL(link('cherry-studio'));
    expect(url.protocol).toBe('cherrystudio:');
    expect(JSON.parse(atob(url.searchParams.get('data') || ''))).toEqual({
      apiKey: 'sk-lazu-abc123',
      baseUrl: 'https://api.lazu.ai',
      id: 'lazu',
      name: 'Lazu',
      type: 'openai',
    });
    expect(describeDeepLink(link('cherry-studio'))).toContain(
      '"baseUrl": "https://api.lazu.ai"',
    );
  });

  it('builds the OpenCat link', () => {
    const opencat = new URL(link('opencat'));
    expect(opencat.searchParams.get('domain')).toBe('https://api.lazu.ai');
    expect(opencat.searchParams.get('token')).toBe('sk-lazu-abc123');
  });

  it('returns nothing for tools without an import link', () => {
    expect(link('cursor')).toBe('');
    expect(link('openai-sdk')).toBe('');
  });

  it('offers no link that would put the Key on the network', () => {
    CONNECT_TOOLS.forEach((item) => {
      const url = buildDeepLink(item, {
        ...ctx,
        homepage: 'https://lazu.ai',
      });
      if (!url) return;
      expect(url.startsWith('http://') || url.startsWith('https://')).toBe(
        false,
      );
    });
  });
});

describe('the address the console trusts', () => {
  // PUBLIC_SERVER_ADDRESS is set by whoever runs the deployment and ends up
  // inside a command the user pastes into a terminal.
  it('accepts a plain origin', () => {
    [
      'https://api.lazu.ai',
      'http://localhost:3000',
      'https://gw.example.com/lazu',
      'https://[2001:db8::1]:8443',
    ].forEach((value) => expect(isUsableOrigin(value)).toBe(true));
  });

  it('refuses anything a shell would read as more than one word', () => {
    [
      'https://api.example.com; echo pwned',
      'https://api.example.com/$(id)',
      'https://api.example.com/`id`',
      'https://user:pass@api.example.com',
      'https://api.example.com/?x=1',
      'https://api.example.com/#f',
      'ftp://api.example.com',
      'api.example.com',
      '',
    ].forEach((value) => expect(isUsableOrigin(value)).toBe(false));
  });
});

describe('loopback origins', () => {
  // The shipped config.yaml says http://localhost:3000, and a self-hosted
  // deployment may leave any other loopback spelling there. None of them may
  // be written into a stranger's installer.
  it('recognises every spelling of the loopback range', () => {
    [
      'http://localhost:3000',
      'http://sub.localhost',
      'http://127.0.0.1:3000',
      'http://127.1.2.3',
      'http://[::1]:3000',
      'http://[0:0:0:0:0:0:0:1]:3000',
      'http://[0000:0000:0000:0000:0000:0000:0000:0001]',
      // URL canonicalizes this one to [::ffff:7f00:1] before anybody sees it.
      'http://[::ffff:127.0.0.1]:3000',
      'http://[::ffff:7f00:1]',
      'http://[0:0:0:0:0:ffff:127.0.0.1]',
    ].forEach((origin) => expect(isLoopbackOrigin(origin)).toBe(true));
  });

  it('leaves real addresses alone', () => {
    [
      'https://api.lazu.ai',
      'https://127.example.com',
      'https://[2001:db8::1]:8443',
      'https://128.0.0.1',
      'https://[::ffff:128.0.0.1]',
      'https://[::ffff:8.8.8.8]',
      'not a url',
    ].forEach((origin) => expect(isLoopbackOrigin(origin)).toBe(false));
  });
});

describe('key label', () => {
  it('keeps the masked prefix and tail', () => {
    expect(shortKeyLabel({ masked_key: 'sk-lazu-972y...XlMg' })).toBe(
      'sk-lazu-972y…XlMg',
    );
    expect(shortKeyLabel({ masked_key: '************e6f7' })).toBe('sk-…e6f7');
    expect(shortKeyLabel({})).toBe('sk-…');
  });

  it('treats only digest-only Keys as legacy', () => {
    expect(isLegacyKey({ secret_available: false })).toBe(true);
    expect(isLegacyKey({ secret_available: true, can_reveal_key: false })).toBe(
      false,
    );
    expect(isLegacyKey({ can_reveal_key: false })).toBe(true);
  });
});

describe('model control', () => {
  it('hides the model for import links that do not carry one', () => {
    expect(
      CONNECT_TOOLS.filter((item) => !item.usesModel).map((item) => item.id),
    ).toEqual(['cherry-studio', 'opencat', 'lobechat']);
  });
});

describe('models beyond the default and light ones', () => {
  const slots = {
    ...ctx,
    extras: ['claude-opus-5', 'deepseek-v4-flash', 'gpt-5.2', 'gpt-5.2'],
    opus: 'claude-opus-5',
    sonnet: 'claude-sonnet-5',
  };

  it('gives Claude Code its opus and sonnet aliases everywhere', () => {
    const sh = buildSnippet(tool('claude-code'), 'script', slots, t);
    expect(sh).toContain(
      "--model 'claude-sonnet-5' --opus 'claude-opus-5' --sonnet 'claude-sonnet-5' --haiku 'deepseek-v4-flash'",
    );
    // Claude Code holds aliases, not a list: the extras go nowhere.
    expect(sh).not.toContain('--also');
    const pwsh = buildSnippet(tool('claude-code'), 'powershell', slots, t);
    expect(pwsh).toContain(
      "-Model 'claude-sonnet-5' -Opus 'claude-opus-5' -Sonnet 'claude-sonnet-5' -Haiku 'deepseek-v4-flash'",
    );
    expect(pwsh).not.toContain('-Also');
    const settings = buildSnippet(tool('claude-code'), 'settings', slots, t);
    const json = JSON.parse(settings.split('\n').slice(1).join('\n'));
    expect(json.env.ANTHROPIC_DEFAULT_OPUS_MODEL).toBe('claude-opus-5');
    expect(json.env.ANTHROPIC_DEFAULT_SONNET_MODEL).toBe('claude-sonnet-5');
    expect(json.env.ANTHROPIC_DEFAULT_HAIKU_MODEL).toBe('deepseek-v4-flash');
    const env = buildSnippet(tool('claude-code'), 'env', slots, t);
    expect(env).toContain(
      "export ANTHROPIC_DEFAULT_OPUS_MODEL='claude-opus-5'",
    );
    expect(env).toContain(
      "export ANTHROPIC_DEFAULT_SONNET_MODEL='claude-sonnet-5'",
    );
  });

  it('puts the extras into a list tool once each, after the two defaults', () => {
    expect(providerModels(tool('opencode'), slots)).toEqual([
      'claude-sonnet-5',
      'deepseek-v4-flash',
      'claude-opus-5',
      'gpt-5.2',
    ]);
    // OpenClaw has no light slot, so the light model is not on its list.
    expect(providerModels(tool('openclaw'), slots)).toEqual([
      'claude-sonnet-5',
      'claude-opus-5',
      'deepseek-v4-flash',
      'gpt-5.2',
    ]);
    // Gemini CLI takes one model.
    expect(providerModels(tool('gemini-cli'), slots)).toEqual([
      'claude-sonnet-5',
    ]);

    const sh = buildSnippet(tool('opencode'), 'script', slots, t);
    expect(sh).toContain(
      "--model 'claude-sonnet-5' --small 'deepseek-v4-flash' --also 'claude-opus-5' --also 'gpt-5.2'",
    );
    expect(sh).not.toContain('--opus');
    const pwsh = buildSnippet(tool('openclaw'), 'powershell', slots, t);
    expect(pwsh).toContain(
      "-Model 'claude-sonnet-5' -Also 'claude-opus-5','deepseek-v4-flash','gpt-5.2'",
    );

    const opencode = buildSnippet(tool('opencode'), 'json', slots, t);
    const config = JSON.parse(opencode.split('\n').slice(1).join('\n'));
    expect(Object.keys(config.provider.lazu.models)).toEqual([
      'claude-sonnet-5',
      'deepseek-v4-flash',
      'claude-opus-5',
      'gpt-5.2',
    ]);
    expect(config.model).toBe('lazu/claude-sonnet-5');
    expect(config.small_model).toBe('lazu/deepseek-v4-flash');

    const openclaw = buildSnippet(tool('openclaw'), 'json', slots, t);
    const claw = JSON.parse(openclaw.split('\n').slice(1).join('\n'));
    expect(
      claw.models.providers.lazu.models.map((m: { id: string }) => m.id),
    ).toEqual([
      'claude-sonnet-5',
      'claude-opus-5',
      'deepseek-v4-flash',
      'gpt-5.2',
    ]);
    expect(claw.agents.defaults.model).toBe('lazu/claude-sonnet-5');

    const gemini = buildSnippet(tool('gemini-cli'), 'script', slots, t);
    expect(gemini).not.toContain('--also');
    expect(gemini).not.toContain('--opus');
  });

  it('sends Codex extras to the script and names them in config.toml', () => {
    const codexSlots = { ...slots, model: 'gpt-5.2', extras: ['kimi-k3'] };
    const sh = buildSnippet(tool('codex'), 'script', codexSlots, t);
    expect(sh).toContain("--model 'gpt-5.2' --also 'kimi-k3'");
    // The catalog file follows the installed Codex, so the toml tab never
    // carries one: it names the extras and leaves the file to the script.
    const toml = buildSnippet(tool('codex'), 'toml', codexSlots, t);
    expect(toml).not.toContain('model_catalog_json');
    expect(toml).toContain('# page.console.token.connect.snippet.codexCatalog');
    const alone = buildSnippet(
      tool('codex'),
      'toml',
      { ...ctx, extras: [], model: 'gpt-5.2' },
      t,
    );
    expect(alone).not.toContain('codexCatalog');
  });

  it('keeps the OpenClaw list to the default model when nothing else is set', () => {
    const openclaw = buildSnippet(
      tool('openclaw'),
      'json',
      { ...ctx, light: '' },
      t,
    );
    expect(openclaw).toContain(
      '"models": [{ "id": "claude-sonnet-5", "name": "claude-sonnet-5" }]',
    );
  });

  it('sends the alias slots to CC Switch for Claude only', () => {
    const claude = new URL(
      buildDeepLink(tool('claude-code'), {
        ...slots,
        homepage: 'https://lazu.ai',
      }),
    );
    expect(claude.searchParams.get('opusModel')).toBe('claude-opus-5');
    expect(claude.searchParams.get('sonnetModel')).toBe('claude-sonnet-5');
    expect(claude.searchParams.get('haikuModel')).toBe('deepseek-v4-flash');
    // Its other apps take one model: nothing extra rides on the link.
    for (const id of ['codex', 'opencode', 'openclaw'] as const) {
      const url = new URL(
        buildDeepLink(tool(id), { ...slots, homepage: 'https://lazu.ai' }),
      );
      expect(url.searchParams.get('opusModel')).toBeNull();
      expect(url.searchParams.get('sonnetModel')).toBeNull();
      expect(url.searchParams.get('model')).toBe('claude-sonnet-5');
    }
  });
});

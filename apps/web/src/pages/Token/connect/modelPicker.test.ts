import { describe, expect, it } from 'vitest';
import {
  buildPicker,
  classifyTier,
  DEFAULT_EXTRA_LIMIT,
  defaultAliasModels,
  defaultExtraModels,
  priceLabel,
  variantBaseOf,
  defaultLightModel,
  defaultModel,
  findPickerModel,
  groupKeyOf,
  groupToRevealFor,
  isChatModel,
  laneOffer,
  releaseLabel,
  toolEndpointLabel,
  vendorLabel,
  type PickerFact,
} from './modelPicker';
import type { ConnectToolId } from './tools';

const day = (iso: string) => Math.floor(Date.parse(`${iso}T00:00:00Z`) / 1000);

const fact = (model: string, extra: Partial<PickerFact> = {}): PickerFact => ({
  endpointTypes: ['openai', 'openai-response'],
  model,
  ...extra,
});

/** A slice of the production catalog, with the vendors it actually reports. */
const VENDORS = new Map([
  ['v-anthropic', 'Anthropic'],
  ['v-openai', 'OpenAI'],
  ['v-google', 'Google'],
  ['v-deepseek', 'DeepSeek'],
  ['v-moonshot', 'Moonshot AI (China)'],
  ['v-nvidia', 'Nvidia'],
]);

const CATALOG: PickerFact[] = [
  fact('claude-fable-5-1', {
    endpointTypes: ['anthropic', 'openai'],
    outputPerMtok: 50,
    releasedAt: day('2026-09-10'),
    vendorID: 'v-anthropic',
  }),
  fact('claude-opus-5', {
    endpointTypes: ['anthropic', 'openai'],
    outputPerMtok: 25,
    releasedAt: day('2026-08-03'),
    vendorID: 'v-anthropic',
  }),
  fact('claude-sonnet-5', {
    endpointTypes: ['anthropic', 'openai'],
    outputPerMtok: 10,
    releasedAt: day('2026-07-17'),
    vendorID: 'v-anthropic',
  }),
  fact('claude-haiku-4-5', {
    endpointTypes: ['anthropic', 'openai'],
    outputPerMtok: 5,
    releasedAt: day('2025-10-15'),
    vendorID: 'v-anthropic',
  }),
  fact('gpt-6-astra', {
    outputPerMtok: 50,
    releasedAt: day('2026-09-10'),
    vendorID: 'v-openai',
  }),
  fact('gpt-5.4-mini', {
    outputPerMtok: 4.5,
    releasedAt: day('2026-03-17'),
    vendorID: 'v-openai',
  }),
  fact('gpt-5.4-mini-2026-03-17', {
    outputPerMtok: 4.5,
    releasedAt: day('2026-03-17'),
    vendorID: 'v-openai',
  }),
  fact('gemini-3.8-flash', {
    endpointTypes: ['gemini', 'openai'],
    outputPerMtok: 3.75,
    releasedAt: day('2026-09-10'),
    vendorID: 'v-google',
  }),
  fact('gemini-2.5-pro', {
    endpointTypes: ['gemini', 'openai'],
    outputPerMtok: 10,
    releasedAt: day('2025-06-17'),
    vendorID: 'v-google',
  }),
  fact('gemini-3.5-flash-lite', {
    endpointTypes: ['gemini', 'openai'],
    outputPerMtok: 2.5,
    vendorID: 'v-google',
  }),
  fact('deepseek-v4-pro', {
    outputPerMtok: 2.475,
    releasedAt: day('2026-04-24'),
    vendorID: 'v-deepseek',
  }),
  fact('kimi-k3', {
    outputPerMtok: 15,
    releasedAt: day('2026-08-03'),
    vendorID: 'v-moonshot',
  }),
  fact('text-embedding-3-large', {
    modalityOutput: ['text'],
    outputPerMtok: 0.13,
    vendorID: 'v-nvidia',
  }),
  fact('gpt-image-2', {
    modalityOutput: ['text', 'image'],
    vendorID: 'v-openai',
  }),
  fact('gemini-3.1-flash-tts-preview', {
    endpointTypes: ['gemini', 'openai'],
    modalityOutput: ['text', 'audio'],
    vendorID: 'v-google',
  }),
];

const pick = (toolId: ConnectToolId, extra = {}) =>
  buildPicker({
    facts: CATALOG,
    toolId,
    vendorNames: VENDORS,
    ...extra,
  });

const idsOf = (result: ReturnType<typeof pick>) => result.all;

describe('what the picker offers', () => {
  it('excludes decision endpoints and decision output from chat tools', () => {
    const facts = [
      fact('gpt-6-astra'),
      fact('jev-1.13', { endpointTypes: ['decisions'] }),
      fact('clef', { modalityOutput: ['decision'] }),
    ];
    for (const toolId of ['codex', 'claude-code', 'gemini-cli'] as const) {
      expect(buildPicker({ facts, toolId }).all).toEqual(['gpt-6-astra']);
    }
  });
  it('requires chat support for an OpenAI bridge but keeps Responses for Codex', () => {
    const facts = [
      fact('o3-pro', { endpointTypes: ['openai-response'] }),
      fact('gpt-6-astra'),
    ];
    for (const toolId of ['claude-code', 'gemini-cli'] as const) {
      const result = buildPicker({ facts, toolId });
      expect(result.all).toEqual(['gpt-6-astra']);
      expect(defaultModel(result)).toBe('gpt-6-astra');
    }
    expect(buildPicker({ facts, toolId: 'codex' }).all).toContain('o3-pro');
  });

  it.each([
    ['imagen-4.0-generate-001', 'image-generation'],
    ['ideogram_generate_V_2_TURBO', 'image-generation'],
    ['custom-video', 'video-generation'],
    ['custom-vector', 'embeddings'],
    ['custom-ranker', 'jina-rerank'],
  ])('rejects %s even when its endpoints include chat', (model, endpoint) => {
    const facts = [
      fact(model, { endpointTypes: [endpoint, 'openai', 'openai-response'] }),
    ];
    expect(isChatModel(facts[0])).toBe(false);
    for (const toolId of [
      'claude-code',
      'gemini-cli',
      'codex',
      'openai-sdk',
    ] as const) {
      const result = buildPicker({ facts, toolId });
      expect(result.all).toEqual([]);
      expect(defaultModel(result)).toBe('');
    }
  });

  it('drops a model the catalog no longer describes', () => {
    // The bug this replaces: /api/user/models is the union of model names the
    // enabled channels carry, so an id the catalog dropped still arrived —
    // with no protocol, so nothing ruled it out, and sorting before every
    // lowercase letter it led the list.
    const result = pick('claude-code', {
      channelModels: ['@cf/baai/bge-m3', 'claude-fable-5-1'],
    });
    expect(idsOf(result)).not.toContain('@cf/baai/bge-m3');
    expect(result.hidden.unlisted).toBe(1);
    expect(defaultModel(result)).toBe('claude-fable-5-1');
  });

  it('keeps embeddings, image and audio models out of every tool', () => {
    expect(
      isChatModel(CATALOG.find((f) => f.model.includes('embedding'))!),
    ).toBe(false);
    const tools: ConnectToolId[] = [
      'claude-code',
      'codex',
      'gemini-cli',
      'openai-sdk',
    ];
    tools.forEach((toolId) => {
      const ids = idsOf(pick(toolId));
      expect(ids).not.toContain('text-embedding-3-large');
      expect(ids).not.toContain('gpt-image-2');
      expect(ids).not.toContain('gemini-3.1-flash-tts-preview');
    });
    const hidden = pick('claude-code').hidden;
    expect(hidden.embedding).toBe(1);
    expect(hidden.image).toBe(1);
    expect(hidden.audio).toBe(1);
  });

  it('offers other vendors to a tool that has its own', () => {
    // Claude Code speaks the Anthropic protocol, and every channel family
    // converts a Claude request, so the choice is the user's.
    const ids = idsOf(pick('claude-code'));
    expect(ids).toContain('gpt-6-astra');
    expect(ids).toContain('gemini-3.8-flash');
    expect(ids).toContain('kimi-k3');
  });

  it('keeps a model off a tool whose format its channel cannot serve', () => {
    // An xAI channel declares SupportsClaude false and implements the OpenAI
    // converter alone, so the catalog marks its models `openai` and nothing
    // else. Offering one to Claude Code would write a config that fails on
    // the first request.
    const xaiOnly: PickerFact[] = [
      fact('grok-5', {
        endpointTypes: ['openai'],
        outputPerMtok: 15,
        releasedAt: day('2026-08-01'),
        vendorID: 'v-xai',
      }),
      fact('claude-opus-5', {
        endpointTypes: ['anthropic', 'openai'],
        outputPerMtok: 25,
        releasedAt: day('2026-08-03'),
        vendorID: 'v-anthropic',
      }),
    ];
    const vendors = new Map([
      ['v-xai', 'xAI'],
      ['v-anthropic', 'Anthropic'],
    ]);
    const forTool = (toolId: ConnectToolId) =>
      buildPicker({ facts: xaiOnly, toolId, vendorNames: vendors }).all;
    expect(forTool('claude-code')).not.toContain('grok-5');
    expect(forTool('codex')).not.toContain('grok-5');
    expect(forTool('gemini-cli')).not.toContain('grok-5');
    // The OpenAI-shaped tools reach it, because that converter is the one
    // every channel family implements.
    expect(forTool('openai-sdk')).toContain('grok-5');
    expect(forTool('cursor')).toContain('grok-5');
  });

  it('names a protocol in the empty state for every tool', () => {
    const tool = (id: ConnectToolId) =>
      ({ id }) as unknown as Parameters<typeof toolEndpointLabel>[0];
    expect(toolEndpointLabel(tool('codex'))).toBe('openai-response');
    // A tool with no requirement of its own still speaks OpenAI chat; an
    // empty label left the warning reading "no  model available".
    expect(toolEndpointLabel(tool('openai-sdk'))).toBe('openai');
  });

  it('holds Codex and Gemini CLI to the protocols they can be served on', () => {
    // Responses needs an OpenAI-family channel, which is what
    // `openai-response` marks; Claude models on an Anthropic channel never
    // carry it.
    expect(idsOf(pick('codex'))).not.toContain('claude-fable-5-1');
    expect(idsOf(pick('codex'))).toContain('gpt-6-astra');
    // The Gemini format is implemented by the Gemini and OpenAI adaptors only.
    expect(idsOf(pick('gemini-cli'))).not.toContain('claude-opus-5');
    expect(idsOf(pick('gemini-cli'))).toContain('gemini-3.8-flash');
    expect(idsOf(pick('gemini-cli'))).toContain('deepseek-v4-pro');
  });

  it('narrows to the Key scope and skips vendors the Key refuses', () => {
    const scoped = pick('claude-code', {
      allowed: ['claude-opus-5', 'gpt-6-astra'],
    });
    expect(idsOf(scoped)).toEqual(['claude-opus-5', 'gpt-6-astra']);
    const blocked = pick('claude-code', {
      blockedVendors: new Set(['v-openai']),
    });
    expect(idsOf(blocked)).not.toContain('gpt-6-astra');
  });
});

describe('the order models come in', () => {
  it('keeps nested variants selectable regardless of catalog order', () => {
    const ids = [
      'gemini-3.1-pro',
      'gemini-3.1-pro-preview',
      'gemini-3.1-pro-preview-customtools',
    ];
    for (const order of [ids, [...ids].reverse()]) {
      const args = {
        facts: order.map((model) => fact(model, { ownerBy: 'Google' })),
        toolId: 'gemini-cli' as const,
      };
      const result = buildPicker(args);
      expect(new Set(result.all)).toEqual(new Set(ids));
      expect(result.sections[0].groups[0].models).toHaveLength(1);
      expect(
        result.sections[0].groups[0].models[0].variants.map((m) => m.id),
      ).toEqual(ids.slice(1));
      const leaf = ids[2];
      const searched = buildPicker({ ...args, query: 'customtools' });
      expect(searched.all).toContain(leaf);
      expect(findPickerModel(searched, leaf)?.id).toBe(leaf);
      expect(groupKeyOf(searched, leaf)).toBe('google');
      expect(groupToRevealFor(result, leaf, 4)).toBe('google');
      expect(buildPicker({ ...args, allowed: [leaf] }).all).toEqual([leaf]);
    }
  });

  it('keeps the configured maker order for every tool and resolves all maker labels', () => {
    for (const toolId of ['claude-code', 'codex', 'gemini-cli'] as const) {
      const result = buildPicker({
        toolId,
        facts: [
          fact('gpt-example', {
            maker: 'openai',
            makerRank: 2,
            ownerBy: 'OpenRouter',
          }),
          fact('mistral-example', {
            maker: 'mistral',
            makerRank: 1,
            ownerBy: 'OpenRouter',
          }),
          fact('llama-example', {
            maker: 'meta',
            makerRank: 0,
            ownerBy: 'OpenRouter',
          }),
        ],
      });
      expect(
        result.sections.flatMap((s) => s.groups).map((g) => g.label),
      ).toEqual(['Meta', 'Mistral', 'OpenAI']);
      expect(result.sections[0].groups[0].vendorName).toBe('Meta');
      expect(defaultModel(result)).toBe('llama-example');
    }
  });

  it('sorts each vendor newest first and puts undated models last', () => {
    const google = pick('gemini-cli')
      .sections.flatMap((s) => s.groups)
      .find((g) => g.key === 'google')!;
    expect(google.models.map((model) => model.id)).toEqual([
      'gemini-3.8-flash',
      'gemini-2.5-pro',
      'gemini-3.5-flash-lite',
    ]);
    expect(releaseLabel(google.models[0])).toBe('2026-09-10');
    expect(releaseLabel(google.models[2])).toBe('');
  });

  it('folds a dated id under the model it dates', () => {
    expect(variantBaseOf('gpt-5.4-mini-2026-03-17')).toBe('gpt-5.4-mini');
    expect(variantBaseOf('claude-haiku-4-5')).toBe('');
    const openai = pick('codex')
      .sections.flatMap((s) => s.groups)
      .find((g) => g.key === 'openai')!;
    const ids = openai.models.map((model) => model.id);
    expect(ids).toContain('gpt-5.4-mini');
    // The snapshot leaves the vendor's top-level rows...
    expect(ids).not.toContain('gpt-5.4-mini-2026-03-17');
    expect(
      openai.models
        .find((model) => model.id === 'gpt-5.4-mini')
        ?.variants.map((variant) => variant.id),
    ).toEqual(['gpt-5.4-mini-2026-03-17']);
    // ...but it is still something the field can land on. Folded, not
    // discarded: the picker shows it once the group is open.
    expect(pick('codex').all).toContain('gpt-5.4-mini-2026-03-17');
  });

  it('finds a folded variant by its own id', () => {
    const searched = buildPicker({
      facts: CATALOG,
      query: 'gpt-5.4-mini-2026',
      toolId: 'codex',
      vendorNames: VENDORS,
    });
    // Searching the snapshot keeps the model it belongs to, so the row it
    // hangs off is on screen.
    expect(searched.all).toContain('gpt-5.4-mini');
    expect(searched.all).toContain('gpt-5.4-mini-2026-03-17');
    expect(findPickerModel(searched, 'gpt-5.4-mini-2026-03-17')?.id).toBe(
      'gpt-5.4-mini-2026-03-17',
    );
    expect(groupKeyOf(searched, 'gpt-5.4-mini-2026-03-17')).toBe('openai');
  });

  it('names the group the field must expand to show a choice', () => {
    const codex = pick('codex');
    const openai = codex.sections
      .flatMap((s) => s.groups)
      .find((g) => g.key === 'openai')!
      .models.map((m) => m.id);
    // A model the resting list already shows needs no expansion.
    expect(groupToRevealFor(codex, openai[0], 4)).toBeUndefined();
    // One past the fold, and a variant folded under another model, do.
    if (openai.length > 4) {
      expect(groupToRevealFor(codex, openai[4], 4)).toBe('openai');
    }
    expect(groupToRevealFor(codex, 'gpt-5.4-mini-2026-03-17', 4)).toBe(
      'openai',
    );
    expect(groupToRevealFor(codex, '', 4)).toBeUndefined();
    expect(groupToRevealFor(codex, 'not-a-model', 4)).toBeUndefined();
  });

  it('folds a named variant, but never a newer version', () => {
    const offered = new Set([
      'claude-fable-5',
      'claude-fable-5-1',
      'gemini-3.1-pro-preview',
      'gemini-3.1-pro-preview-customtools',
    ]);
    expect(variantBaseOf('gemini-3.1-pro-preview-customtools', offered)).toBe(
      'gemini-3.1-pro-preview',
    );
    // `-1` is a version, so the newer model keeps its own row.
    expect(variantBaseOf('claude-fable-5-1', offered)).toBe('');
    // The base has to be on offer before anything folds under it.
    expect(variantBaseOf('gemini-3.1-pro-preview', offered)).toBe('');
  });

  it('reads a size off the name, then off the output price', () => {
    expect(classifyTier(fact('claude-opus-5'))).toBe('flagship');
    expect(classifyTier(fact('gpt-6-astra'))).toBe('flagship');
    expect(classifyTier(fact('claude-haiku-4-5'))).toBe('light');
    expect(classifyTier(fact('gemini-3.8-flash'))).toBe('light');
    expect(classifyTier(fact('claude-sonnet-5', { outputPerMtok: 10 }))).toBe(
      'balanced',
    );
    expect(classifyTier(fact('kimi-k3', { outputPerMtok: 15 }))).toBe(
      'flagship',
    );
    expect(classifyTier(fact('house-model', { outputPerMtok: 1 }))).toBe(
      'light',
    );
  });

  it('matches a size word, not a substring of the vendor name', () => {
    // `gemini` ends in `mini`: a substring test made every Gemini model a
    // light one, including the Pro.
    expect(classifyTier(fact('gemini-3.1-pro-preview'))).toBe('flagship');
    expect(classifyTier(fact('gemini-3.5-flash-lite'))).toBe('light');
    expect(classifyTier(fact('MiniMax-M2.7', { outputPerMtok: 1.2 }))).toBe(
      'light',
    );
    expect(
      classifyTier(fact('MiniMax-M2.5-highspeed', { outputPerMtok: 2.45 })),
    ).toBe('balanced');
  });

  it('prints a price a reader can compare', () => {
    const model = (input: number, output: number) => ({
      id: 'm',
      inputPerMtok: input,
      outputPerMtok: output,
      tier: 'balanced' as const,
      variants: [],
      vendorLabel: 'V',
    });
    expect(priceLabel(model(10, 50))).toBe('$10 / $50');
    expect(priceLabel(model(10, 50), 'cheap')).toBe('');
    expect(priceLabel(model(10, 50), 'direct')).toBe('$10 / $50');
    // A reseller rate arrives with a repeating tail.
    expect(priceLabel(model(1.1176470588, 4.7058823529))).toBe('$1.12 / $4.71');
    expect(priceLabel(model(0.1875, 0.75))).toBe('$0.19 / $0.75');
    // Two decimals would round these away, so they keep more.
    expect(priceLabel(model(0.002, 0.0004))).toBe('$0.002 / $0.0004');
  });
});

describe('what the two fields open on', () => {
  it.each(['2026-09-10', 'thinking'])(
    'keeps a %s variant light default in its vendor',
    (suffix) => {
      const snapshot = `gpt-6-astra-${suffix}`;
      const result = pick('claude-code', {
        facts: [...CATALOG, fact(snapshot, { vendorID: 'v-openai' })],
      });
      expect(defaultLightModel(result, snapshot)).toBe('gpt-5.4-mini');
      const noLight = pick('claude-code', {
        facts: result.args.facts.filter(
          (entry) => !entry.model.startsWith('gpt-5.4-mini'),
        ),
      });
      expect(defaultLightModel(noLight, snapshot)).toBe(snapshot);
    },
  );

  it('does not switch vendors when a main model is absent from the catalog', () => {
    expect(defaultLightModel(pick('claude-code'), 'removed-model')).toBe(
      'removed-model',
    );
  });

  it('opens on the newest flagship of the recommended vendor', () => {
    expect(defaultModel(pick('claude-code'))).toBe('claude-fable-5-1');
    expect(defaultModel(pick('codex'))).toBe('gpt-6-astra');
    // No hand-written model list decides this, so a catalog with different
    // ids still gets a sensible default.
    const future = buildPicker({
      facts: [
        fact('claude-atlas-9', {
          endpointTypes: ['anthropic'],
          outputPerMtok: 60,
          releasedAt: day('2027-01-01'),
          vendorID: 'v-anthropic',
        }),
        fact('claude-opus-5', {
          endpointTypes: ['anthropic'],
          outputPerMtok: 25,
          releasedAt: day('2026-08-03'),
          vendorID: 'v-anthropic',
        }),
      ],
      toolId: 'claude-code',
      vendorNames: VENDORS,
    });
    expect(defaultModel(future)).toBe('claude-atlas-9');
  });

  it('keeps the default inside the leading vendor', () => {
    // Gemini CLI with no Google models: the first section is the other-vendor
    // list, whose leading group is Anthropic. Flattening that section would
    // have handed the default to a later vendor's flagship.
    const noGoogle = buildPicker({
      facts: [
        fact('claude-sonnet-5', {
          endpointTypes: ['gemini'],
          outputPerMtok: 10,
          releasedAt: day('2026-07-17'),
          vendorID: 'v-anthropic',
        }),
        fact('gpt-6-astra', {
          outputPerMtok: 50,
          releasedAt: day('2026-09-10'),
          vendorID: 'v-openai',
        }),
      ],
      toolId: 'gemini-cli',
      vendorNames: VENDORS,
    });
    expect(noGoogle.sections[0].groups[0].label).toBe('Anthropic');
    expect(defaultModel(noGoogle)).toBe('claude-sonnet-5');
  });

  it('keeps the main model when its vendor has nothing lighter', () => {
    // Falling through to the whole catalog here would send half a Claude
    // agent's traffic to another vendor without the user choosing it.
    const flagshipsOnly = buildPicker({
      facts: [
        fact('claude-opus-5', {
          endpointTypes: ['anthropic'],
          outputPerMtok: 25,
          releasedAt: day('2026-08-03'),
          vendorID: 'v-anthropic',
        }),
        fact('gpt-5.6-luna', {
          outputPerMtok: 1.2,
          releasedAt: day('2026-07-17'),
          vendorID: 'v-openai',
        }),
      ],
      toolId: 'claude-code',
      vendorNames: VENDORS,
    });
    expect(flagshipsOnly.all).toContain('gpt-5.6-luna');
    expect(defaultLightModel(flagshipsOnly, 'claude-opus-5')).toBe(
      'claude-opus-5',
    );
  });

  it('opens the light field on the main model’s own vendor', () => {
    const result = pick('claude-code');
    expect(defaultLightModel(result, 'claude-fable-5-1')).toBe(
      'claude-haiku-4-5',
    );
    // Pick another vendor's main model and the side tasks follow it there.
    expect(defaultLightModel(result, 'gemini-2.5-pro')).toBe(
      'gemini-3.8-flash',
    );
  });

  it('offers the whole catalog in the light field too', () => {
    // Both fields are the user's to set: the light list is not narrowed to
    // the main model's vendor, it only opens there.
    const result = pick('claude-code');
    expect(idsOf(result)).toContain('claude-haiku-4-5');
    expect(idsOf(result)).toContain('gpt-5.4-mini');
    expect(idsOf(result)).toContain('deepseek-v4-pro');
  });

  it('leaves nothing selected when the Key can call nothing', () => {
    const none = pick('claude-code', { allowed: ['retired-model'] });
    expect(idsOf(none)).toEqual([]);
    expect(defaultModel(none)).toBe('');
  });
});

describe('vendor labels', () => {
  it('prints a brand rather than the routing name', () => {
    expect(vendorLabel('Moonshot AI (China)')).toBe('Moonshot AI');
    expect(vendorLabel('aliyun-bailian')).toBe('Alibaba Cloud');
    expect(vendorLabel('Zhipu AI Coding Plan')).toBe('Zhipu AI');
    expect(vendorLabel('Vultr')).toBe('Vultr');
    expect(vendorLabel('')).toBe('Other');
  });
});

describe('the models beyond the default and light ones', () => {
  it('fills the opus and sonnet slots from the main model vendor by tier', () => {
    const result = pick('claude-code');
    expect(defaultAliasModels(result, 'claude-sonnet-5')).toEqual({
      opus: 'claude-fable-5-1',
      sonnet: 'claude-sonnet-5',
    });
    // An OpenAI main model keeps the aliases on OpenAI.
    expect(defaultAliasModels(result, 'gpt-5.4-mini')).toEqual({
      opus: 'gpt-6-astra',
      sonnet: 'gpt-5.4-mini',
    });
  });

  it('hands a slot the main model when its vendor has no model of that tier', () => {
    const result = pick('claude-code');
    // DeepSeek here has one balanced model and no flagship.
    expect(defaultAliasModels(result, 'deepseek-v4-pro')).toEqual({
      opus: 'deepseek-v4-pro',
      sonnet: 'deepseek-v4-pro',
    });
    expect(defaultAliasModels(result, 'removed-model')).toEqual({
      opus: 'removed-model',
      sonnet: 'removed-model',
    });
  });

  it('imports the main model vendor, then one newest model per other vendor', () => {
    const result = pick('opencode');
    expect(
      defaultExtraModels(result, 'claude-sonnet-5', 'claude-haiku-4-5'),
    ).toEqual([
      'claude-fable-5-1',
      'claude-opus-5',
      'gpt-6-astra',
      'gemini-3.8-flash',
      'deepseek-v4-pro',
      'kimi-k3',
    ]);
    // The list follows the main model's vendor, not the tool's home vendor,
    // and every other vendor still gets one entry.
    expect(defaultExtraModels(result, 'gpt-6-astra', 'gpt-5.4-mini')).toEqual([
      'claude-fable-5-1',
      'gemini-3.8-flash',
      'deepseek-v4-pro',
      'kimi-k3',
    ]);
    expect(defaultExtraModels(result, 'gemini-2.5-pro', '')).toEqual([
      'gemini-3.8-flash',
      'gemini-3.5-flash-lite',
      'claude-fable-5-1',
      'gpt-6-astra',
      'deepseek-v4-pro',
      'kimi-k3',
    ]);
  });

  it('caps the automatic list and leaves out a model absent from the catalog', () => {
    const many = buildPicker({
      facts: Array.from({ length: 8 }, (_, index) =>
        fact(`claude-m${index}`, {
          endpointTypes: ['anthropic', 'openai'],
          releasedAt: day('2026-01-01') + index,
          vendorID: 'v-anthropic',
        }),
      ),
      toolId: 'opencode',
      vendorNames: VENDORS,
    });
    expect(defaultExtraModels(many, 'claude-m7', 'claude-m6')).toHaveLength(
      DEFAULT_EXTRA_LIMIT,
    );
    // A main model the catalog no longer lists leads with the first vendor.
    expect(
      defaultExtraModels(pick('opencode'), 'removed-model', '').slice(0, 4),
    ).toEqual([
      'claude-fable-5-1',
      'claude-opus-5',
      'claude-sonnet-5',
      'claude-haiku-4-5',
    ]);
  });
});

describe('lane offers', () => {
  const model = (lanes: PickerFact['lanes']) => ({
    id: 'gpt-5.6-terra',
    inputPerMtok: 2,
    lanes,
    outputPerMtok: 12,
    tier: 'balanced' as const,
    variants: [],
    vendorLabel: 'OpenAI',
  });

  it('prices each lane from its own row, never the stable rate', () => {
    const priced = model([
      {
        available: true,
        inputPerMtok: 2,
        key: 'direct',
        outputPerMtok: 12,
      },
      {
        available: true,
        inputPerMtok: 0.36,
        key: 'cheap',
        outputPerMtok: 2.16,
      },
    ]);
    expect(priceLabel(priced, 'direct')).toBe('$2 / $12');
    expect(priceLabel(priced, 'cheap')).toBe('$0.36 / $2.16');
    // A model never priced on the agent lane quotes nothing there.
    expect(priceLabel(model([]), 'cheap')).toBe('');
  });

  it('offers the switch only when the agent lane can serve this tool', () => {
    const open = laneOffer(
      model([
        {
          available: true,
          inputPerMtok: 2,
          key: 'direct',
          outputPerMtok: 12,
        },
        {
          available: true,
          inputPerMtok: 0.36,
          key: 'cheap',
          outputPerMtok: 2.16,
        },
      ]),
      'claude-code',
    );
    expect(open.kind).toBe('choice');
    expect(open.cheapPrice).toBe('$0.36 / $2.16');

    // Priced, live, but the source takes Codex alone: the saving is real and
    // still out of reach here, so the price shows and the switch does not.
    const locked = laneOffer(
      model([
        {
          available: true,
          inputPerMtok: 2,
          key: 'direct',
          outputPerMtok: 12,
        },
        {
          available: true,
          clientOnly: 'codex',
          inputPerMtok: 0.36,
          key: 'cheap',
          outputPerMtok: 2.16,
        },
      ]),
      'claude-code',
    );
    expect(locked.kind).toBe('client-locked');
    expect(locked.lockedTo).toBe('codex');
    expect(locked.cheapPrice).toBe('$0.36 / $2.16');
    expect(
      laneOffer(
        model([
          {
            available: true,
            inputPerMtok: 2,
            key: 'direct',
            outputPerMtok: 12,
          },
          {
            available: true,
            clientOnly: 'codex',
            inputPerMtok: 0.36,
            key: 'cheap',
            outputPerMtok: 2.16,
          },
        ]),
        'codex',
      ).kind,
    ).toBe('choice');
  });

  it('quotes only the agent lane when no stable price exists', () => {
    const cheapOnly = model([
      {
        available: true,
        inputPerMtok: 0.92,
        key: 'cheap',
        outputPerMtok: 4.6,
      },
    ]);
    // The top-level catalog rate is the default cheap price for this model.
    cheapOnly.inputPerMtok = 0.92;
    cheapOnly.outputPerMtok = 4.6;
    expect(priceLabel(cheapOnly, 'direct')).toBe('');
    expect(priceLabel(cheapOnly, 'cheap')).toBe('$0.92 / $4.6');
    expect(laneOffer(cheapOnly, 'claude-code').kind).toBe('agent-lane-only');
  });

  it('shows no control when the agent lane has no price or nothing serving it', () => {
    expect(laneOffer(model([]), 'codex').kind).toBe('direct-only');
    expect(
      laneOffer(
        model([
          {
            available: false,
            inputPerMtok: 0.36,
            key: 'cheap',
            outputPerMtok: 2.16,
          },
        ]),
        'codex',
      ).kind,
    ).toBe('direct-only');
    expect(
      laneOffer(model([{ available: true, key: 'cheap' }]), 'codex').kind,
    ).toBe('direct-only');
    expect(laneOffer(undefined, 'codex').kind).toBe('direct-only');
  });
});

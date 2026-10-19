/** What the connect dialog offers as a model, and in what order.
 *
 *  The candidates come from the pricing catalog, never from the union of
 *  model names the enabled channels happen to carry: a channel may still
 *  list a model the catalog dropped, and such a model has no protocol, no
 *  price and no vendor — it used to lead every tool's dropdown because the
 *  id sorted before every lowercase letter.
 */

import { makerDefaultLabel } from '../../../lib/model-makers';
import type { ConnectTool, ConnectToolId } from './tools';

export type ModelTier = 'balanced' | 'flagship' | 'light';

/** One priced lane of a model, as the catalog publishes it. */
export type PickerLane = {
  available: boolean;
  /** The one client this lane's sources accept, when they accept only one.
   *  A lane can be live and still refuse every tool but this. */
  clientOnly?: string;
  inputPerMtok?: number;
  key: Lane;
  outputPerMtok?: number;
};

export type Lane = 'cheap' | 'direct';

/** One catalog row, reduced to what the picker shows and sorts by. */
export type PickerFact = {
  endpointTypes: string[];
  inputPerMtok?: number;
  lanes?: PickerLane[];
  /** The lab behind the model and its place in the operator's maker order,
   *  as the catalog reports them. They win over guessing from the vendor. */
  maker?: string;
  makerRank?: number;
  model: string;
  modalityOutput?: string[];
  ownerBy?: string;
  outputPerMtok?: number;
  releasedAt?: number;
  vendorID?: string;
};

export type PickerModel = {
  id: string;
  inputPerMtok?: number;
  lanes?: PickerLane[];
  outputPerMtok?: number;
  releasedAt?: number;
  /** Ids folded under this one (`gpt-5.4-mini-2026-03-17`). They are hidden
   *  while the group is at rest and shown, selectable, once it is expanded or
   *  a search is running — folded, not discarded. */
  variants: PickerModel[];
  tier: ModelTier;
  vendorLabel: string;
};

export type PickerGroup = {
  key: string;
  label: string;
  models: PickerModel[];
  /** The vendor name as the catalog spells it, for brand-logo lookup. */
  vendorName: string;
};

export type PickerSection = {
  groups: PickerGroup[];
  kind: 'other' | 'recommended';
  label: string;
};

export type HiddenCounts = {
  audio: number;
  embedding: number;
  image: number;
  /** Ids a channel still serves that the catalog does not list. */
  unlisted: number;
};

/** Models whose output is not a chat reply. `modality_output` catches the
 *  image, video and audio models; the names catch embeddings and rerankers,
 *  which the catalog still reports as text out. */
const NON_CHAT_NAME =
  /(embedding|embed|bge-|rerank|whisper|moderation|-tts|tts-|stable-diffusion|flux|sora|kling|veo|-image$|-image-)/i;

const NON_CHAT_MODALITY = ['audio', 'image', 'video', 'decision'];
const NON_CHAT_ENDPOINTS = [
  'image-generation',
  'video-generation',
  'embeddings',
  'jina-rerank',
  'decisions',
];

/** Protocols a tool can actually be served on, written as the catalog's own
 *  endpoint markers.
 *
 *  `null` means the tool asks nothing beyond "this is a chat model": every
 *  channel family implements the OpenAI chat converter, so that format
 *  reaches any model.
 *
 *  The other three formats do not. A channel only serves them when its
 *  provider profile says so (`packages/domain/providercap/profile.go`), and
 *  the catalog marks exactly those families: `openai-response` is appended
 *  only for an API type that supports Responses, `anthropic` for an Anthropic
 *  channel or a Claude-named model, `gemini` for a Gemini one. An xAI channel
 *  carries none of them — its profile declares `SupportsClaude: false` and its
 *  adaptor implements the OpenAI converter alone — so a Grok model served
 *  only there is offered to the OpenAI-shaped tools and to nothing else. */
export const TOOL_REQUIRED_ENDPOINTS: Record<ConnectToolId, string[] | null> = {
  'cherry-studio': null,
  chatbox: null,
  cline: null,
  'claude-code': ['anthropic', 'gemini', 'openai-response'],
  codex: ['openai-response'],
  cursor: null,
  'gemini-cli': ['gemini', 'openai-response'],
  lobechat: null,
  opencat: null,
  opencode: null,
  openclaw: null,
  'openai-sdk': null,
};

/** The vendor whose models a tool is built around; its models lead the list.
 *  Tools with no vendor of their own lead with Anthropic, the house pick for
 *  coding agents. */
export const TOOL_HOME_VENDOR: Record<ConnectToolId, string> = {
  'cherry-studio': 'anthropic',
  chatbox: 'anthropic',
  cline: 'anthropic',
  'claude-code': 'anthropic',
  codex: 'openai',
  cursor: 'anthropic',
  'gemini-cli': 'google',
  lobechat: 'anthropic',
  opencat: 'anthropic',
  opencode: 'anthropic',
  openclaw: 'anthropic',
  'openai-sdk': 'anthropic',
};

/** Vendors in the order the picker lists them, most-used first. Anything the
 *  list does not name follows, alphabetically — never by "how many models the
 *  vendor has", which reorders the groups every time the catalog changes. */
const VENDOR_ORDER: Array<{ key: string; label: string; match: RegExp }> = [
  { key: 'anthropic', label: 'Anthropic', match: /anthropic|claude/i },
  { key: 'openai', label: 'OpenAI', match: /openai|\bgpt\b/i },
  { key: 'google', label: 'Google', match: /google|gemini|vertex/i },
  { key: 'deepseek', label: 'DeepSeek', match: /deepseek/i },
  { key: 'moonshot', label: 'Moonshot AI', match: /moonshot|kimi/i },
  { key: 'zhipu', label: 'Zhipu AI', match: /zhipu|z\.ai|glm/i },
  {
    key: 'alibaba',
    label: 'Alibaba Cloud',
    match: /aliyun|alibaba|bailian|qwen/i,
  },
  { key: 'xai', label: 'xAI', match: /^xai$|grok/i },
  { key: 'minimax', label: 'MiniMax', match: /minimax/i },
];

/** A vendor's place in the fixed order, or a number past its end. */
export const vendorRank = (name: string): number => {
  const index = VENDOR_ORDER.findIndex((entry) => entry.match.test(name));
  return index < 0 ? VENDOR_ORDER.length : index;
};

export const vendorKey = (name: string): string => {
  const entry = VENDOR_ORDER.find((item) => item.match.test(name));
  return entry ? entry.key : name.trim().toLowerCase() || 'other';
};

/** The vendor name as the picker prints it: the known brands by their own
 *  name, anything else as the catalog spells it minus a parenthetical
 *  qualifier, which is routing detail rather than a brand. */
export const vendorLabel = (name: string): string => {
  const entry = VENDOR_ORDER.find((item) => item.match.test(name));
  if (entry) return entry.label;
  const trimmed = name.replace(/\s*\([^)]*\)\s*$/, '').trim();
  return trimmed || name.trim() || 'Other';
};

const FLAGSHIP_WORDS = new Set([
  'astra',
  'fable',
  'max',
  'opus',
  'pro',
  'sol',
  'ultra',
]);
const LIGHT_WORDS = new Set([
  'air',
  'flash',
  'haiku',
  'lite',
  'luna',
  'mini',
  'nano',
  'small',
  'turbo',
]);

/** A model id read as the words it is built from. Matching whole words rather
 *  than substrings matters more than it looks: `gemini` ends in `mini`, so a
 *  substring test made every Gemini model a light one. */
const modelWords = (id: string): string[] =>
  id
    .toLowerCase()
    .split(/[^a-z]+/)
    .filter(Boolean);

/** No catalog field says how big a model is, so the name decides and the
 *  output price breaks the tie. Both are heuristics; a `tier` column on the
 *  model metadata would replace this outright. */
export const classifyTier = (fact: PickerFact): ModelTier => {
  const words = modelWords(fact.model);
  // Light wins over flagship on a name carrying both (`gpt-pro-mini` would
  // be the small one).
  if (words.some((word) => LIGHT_WORDS.has(word))) return 'light';
  if (words.some((word) => FLAGSHIP_WORDS.has(word))) return 'flagship';
  const output = fact.outputPerMtok;
  if (output !== undefined) {
    if (output >= 15) return 'flagship';
    if (output <= 2) return 'light';
  }
  return 'balanced';
};

const TIER_RANK: Record<ModelTier, number> = {
  flagship: 0,
  balanced: 1,
  light: 2,
};

/** True when the catalog describes something a chat client can talk to. */
export const isChatModel = (fact: PickerFact): boolean => {
  const modality = fact.modalityOutput || [];
  if (modality.some((item) => NON_CHAT_MODALITY.includes(item))) return false;
  if (NON_CHAT_NAME.test(fact.model)) return false;
  const endpoints = fact.endpointTypes || [];
  // Generation markers override the generic OpenAI marker that channels add.
  if (endpoints.some((type) => NON_CHAT_ENDPOINTS.includes(type))) return false;
  if (!endpoints.length) return true;
  return endpoints.some((type) =>
    ['anthropic', 'gemini', 'openai', 'openai-response'].includes(type),
  );
};

/** True when the catalog does not rule this model out for the tool. */
export const modelServesTool = (
  fact: PickerFact,
  toolId: ConnectToolId,
): boolean => {
  if (!isChatModel(fact)) return false;
  const required = TOOL_REQUIRED_ENDPOINTS[toolId];
  if (!required) return true;
  const endpoints = fact.endpointTypes || [];
  // A deployment that reports no protocols at all is not second-guessed.
  if (!endpoints.length) return true;
  return endpoints.some((type) => {
    if (!required.includes(type)) return false;
    // Claude/Gemini requests bridge through Chat Completions, not Responses.
    if (type === 'openai-response' && toolId !== 'codex') {
      return endpoints.includes('openai');
    }
    return true;
  });
};

/** `gpt-5.4-mini-2026-03-17` is the same model as `gpt-5.4-mini`; the dated id
 *  folds under it rather than taking a row of its own. */
const DATED_SUFFIX = /-(\d{4}-\d{2}-\d{2}|\d{8})$/;

/** Words that mark a variant of the model named by the rest of the id. The
 *  list is closed on purpose: folding on any trailing segment would make
 *  `claude-fable-5-1` a variant of `claude-fable-5`, and it is a newer model,
 *  not a flavour of the old one. */
const VARIANT_SUFFIXES = ['customtools', 'latest', 'preview', 'thinking'];

/** The model this id is a variant of, or '' when it names one of its own.
 *  `offered` is consulted for the word suffixes so a model is only folded
 *  under something the list actually holds. */
export const variantBaseOf = (id: string, offered?: Set<string>): string => {
  const dated = DATED_SUFFIX.exec(id);
  if (dated) return id.slice(0, dated.index);
  const cut = id.lastIndexOf('-');
  if (cut <= 0) return '';
  const suffix = id.slice(cut + 1).toLowerCase();
  if (!VARIANT_SUFFIXES.includes(suffix)) return '';
  const base = id.slice(0, cut);
  return offered?.has(base) ? base : '';
};

const byRecency = (left: PickerModel, right: PickerModel): number => {
  // A model with no release date is newer than nothing; it goes last, where
  // an unknown date is least in the way.
  const leftAt = left.releasedAt ?? -1;
  const rightAt = right.releasedAt ?? -1;
  if (leftAt !== rightAt) return rightAt - leftAt;
  const tier = TIER_RANK[left.tier] - TIER_RANK[right.tier];
  if (tier !== 0) return tier;
  return left.id.localeCompare(right.id);
};

export type BuildArgs = {
  /** Model ids the Key may call; empty means every model. */
  allowed?: string[];
  /** Vendor ids the Key refuses. */
  blockedVendors?: Set<string>;
  /** Ids the deployment serves, used only to count what the catalog omits. */
  channelModels?: string[];
  facts: PickerFact[];
  query?: string;
  toolId: ConnectToolId;
  vendorNames?: Map<string, string>;
};

export type PickerResult = {
  /** Every offered id, in the order the sections print them. */
  all: string[];
  /** The arguments this result was built from, so a search can rebuild the
   *  same list with a needle instead of filtering the drawn rows. */
  args: BuildArgs;
  hidden: HiddenCounts;
  sections: PickerSection[];
};

/** The candidate list for one tool, grouped and ordered. */
export function buildPicker(args: BuildArgs): PickerResult {
  const {
    allowed,
    blockedVendors,
    channelModels,
    facts,
    query,
    toolId,
    vendorNames,
  } = args;
  const scope = new Set((allowed || []).filter(Boolean));
  const hidden: HiddenCounts = {
    audio: 0,
    embedding: 0,
    image: 0,
    unlisted: 0,
  };

  const known = new Set(facts.map((fact) => fact.model));
  (channelModels || []).forEach((id) => {
    if (id && !known.has(id)) hidden.unlisted += 1;
  });

  const inScope = facts.filter((fact) => {
    if (scope.size && !scope.has(fact.model)) return false;
    if (fact.vendorID && blockedVendors?.has(fact.vendorID)) return false;
    return true;
  });

  // Count what is dropped for not being a chat model before the tool's own
  // protocol narrows things further, so the footnote reads the same whichever
  // tool is selected.
  inScope.forEach((fact) => {
    if (isChatModel(fact)) return;
    const modality = fact.modalityOutput || [];
    if (modality.includes('image') || modality.includes('video')) {
      hidden.image += 1;
      return;
    }
    if (modality.includes('audio') || /-tts|tts-|whisper/i.test(fact.model)) {
      hidden.audio += 1;
      return;
    }
    hidden.embedding += 1;
  });

  const usable = inScope.filter((fact) => modelServesTool(fact, toolId));

  const vendorNameOf = (fact: PickerFact) =>
    (fact.vendorID && vendorNames?.get(fact.vendorID)) || fact.ownerBy || '';
  const makerKeyOf = (fact: PickerFact) =>
    fact.maker || vendorKey(vendorNameOf(fact));
  const makerLabelOf = (fact: PickerFact) =>
    fact.maker
      ? makerDefaultLabel(fact.maker)
      : vendorLabel(vendorNameOf(fact));
  const toModel = (fact: PickerFact, variants: PickerModel[]): PickerModel => ({
    id: fact.model,
    inputPerMtok: fact.inputPerMtok,
    lanes: fact.lanes,
    outputPerMtok: fact.outputPerMtok,
    releasedAt: fact.releasedAt,
    tier: classifyTier(fact),
    variants,
    vendorLabel: makerLabelOf(fact),
  });

  // Fold a dated or named variant under the model it varies. The variant is
  // kept, not dropped: it hangs off the base and the list shows it once the
  // group is open, so an exact snapshot stays selectable.
  const ids = new Set(usable.map((fact) => fact.model));
  const variantsOf = new Map<string, PickerModel[]>();
  const folded = new Set<string>();
  usable.forEach((fact) => {
    let base = variantBaseOf(fact.model, ids);
    if (!base || !ids.has(base)) return;
    // Flatten descendants under the surviving root so every exact ID is
    // reachable by the existing one-level renderer, search and lookups.
    let parent = variantBaseOf(base, ids);
    while (parent && ids.has(parent)) {
      base = parent;
      parent = variantBaseOf(base, ids);
    }
    folded.add(fact.model);
    variantsOf.set(base, [...(variantsOf.get(base) || []), toModel(fact, [])]);
  });

  const needle = (query || '').trim().toLowerCase();
  const groups = new Map<
    string,
    { label: string; models: PickerModel[]; rank: number; vendorName: string }
  >();

  usable.forEach((fact) => {
    if (folded.has(fact.model)) return;
    const vendorName = fact.maker
      ? makerDefaultLabel(fact.maker)
      : vendorNameOf(fact);
    const key = makerKeyOf(fact);
    const label = makerLabelOf(fact);
    const variants = [...(variantsOf.get(fact.model) || [])].sort((a, b) =>
      a.id.localeCompare(b.id),
    );
    // A search that names a variant keeps the model it belongs to, so the
    // snapshot is reachable through its own id.
    if (
      needle &&
      !fact.model.toLowerCase().includes(needle) &&
      !label.toLowerCase().includes(needle) &&
      !variants.some((variant) => variant.id.toLowerCase().includes(needle))
    ) {
      return;
    }
    const group = groups.get(key) || {
      label,
      models: [],
      rank: fact.makerRank ?? vendorRank(vendorName),
      vendorName,
    };
    group.models.push(toModel(fact, variants));
    groups.set(key, group);
  });

  const ordered = [...groups.entries()]
    .map(([key, group]) => ({
      key,
      label: group.label,
      models: [...group.models].sort(byRecency),
      rank: group.rank,
      vendorName: group.vendorName,
    }))
    .sort((a, b) => {
      if (a.rank !== b.rank) return a.rank - b.rank;
      return a.label.localeCompare(b.label);
    })
    .map(({ rank: _rank, ...group }) => group);

  const sections: PickerSection[] = ordered.length
    ? [{ groups: ordered, kind: 'other', label: '' }]
    : [];

  // Every id the picker can land on, variants included: the dialog reads this
  // to decide whether the tool has anything to offer at all.
  const all: string[] = [];
  sections.forEach((section) =>
    section.groups.forEach((group) =>
      group.models.forEach((model) => {
        all.push(model.id);
        model.variants.forEach((variant) => all.push(variant.id));
      }),
    ),
  );

  return { all, args, hidden, sections };
}

/** The model a tool opens on: the newest flagship the leading vendor has,
 *  then its newest model of any size, then the same two questions asked of
 *  the whole list. Nothing here is a hand-written model list, so a catalog
 *  update moves the default on its own.
 *
 *  The leading vendor is one group, not one section: when the tool's own
 *  vendor serves nothing the first section is the other-vendor list, and
 *  flattening it would let a later vendor's flagship beat the leading
 *  vendor's own balanced model. */
export function defaultModel(result: PickerResult): string {
  const groups = result.sections.flatMap((section) => section.groups);
  const pools = [
    groups.length ? groups[0].models : [],
    groups.flatMap((group) => group.models),
  ];
  for (const pool of pools) {
    const flagship = pool.find((model) => model.tier === 'flagship');
    if (flagship) return flagship.id;
    if (pool.length) return pool[0].id;
  }
  return '';
}

/** The small model a tool uses for its side tasks. It opens on the main
 *  model's own vendor so a Claude agent's side traffic stays on Claude, but
 *  the picker still offers everything — the choice is the user's. */
export function defaultLightModel(
  result: PickerResult,
  mainModel: string,
): string {
  const groups = result.sections.flatMap((section) => section.groups);
  const ownerKey = groupKeyOf(result, mainModel);
  const owner = groups.find((group) => group.key === ownerKey);
  // Once the main model's vendor is known the search stops there: a vendor
  // with nothing lighter to offer gets the main model back, rather than
  // handing half the agent's traffic to a vendor the user did not pick.
  const pool = owner?.models || [];
  const light = pool.find((model) => model.tier === 'light');
  if (light) return light.id;
  const balanced = pool.find((model) => model.tier === 'balanced');
  if (balanced) return balanced.id;
  return mainModel;
}

export type AliasModels = { opus: string; sonnet: string };

/** The two Claude Code alias slots beside Haiku: `/model opus` opens the
 *  main model's vendor's newest flagship, `/model sonnet` its newest balanced
 *  model. A vendor with nothing in a tier hands the slot the main model, the
 *  same fallback the light slot uses, so every alias still answers. */
export function defaultAliasModels(
  result: PickerResult,
  mainModel: string,
): AliasModels {
  const groups = result.sections.flatMap((section) => section.groups);
  const ownerKey = groupKeyOf(result, mainModel);
  const pool = groups.find((group) => group.key === ownerKey)?.models || [];
  const pick = (tier: ModelTier) =>
    pool.find((model) => model.tier === tier)?.id || mainModel;
  return { opus: pick('flagship'), sonnet: pick('balanced') };
}

/** How many of the main model's vendor a list tool imports beside its
 *  default and light ones before the user adds any: the same short list the
 *  picker shows a vendor at rest. */
export const DEFAULT_EXTRA_LIMIT = 4;

/** The models a list tool (Codex, OpenCode, OpenClaw) imports alongside the
 *  default and light ones: the main model's vendor as the picker shows it at
 *  rest, newest first, then the newest model of every other vendor, so the
 *  app's own switcher opens with one entry per vendor the Key can reach —
 *  a gateway Key is for reaching more than one vendor. A longer list is the
 *  user's to build. */
export function defaultExtraModels(
  result: PickerResult,
  mainModel: string,
  lightModel: string,
): string[] {
  const groups = result.sections.flatMap((section) => section.groups);
  const ownerKey = groupKeyOf(result, mainModel);
  const owner = groups.find((group) => group.key === ownerKey) || groups[0];
  if (!owner) return [];
  const taken = new Set([mainModel, lightModel]);
  const fresh = (ids: string[]) => ids.filter((id) => !taken.has(id));
  const own = fresh(owner.models.map((model) => model.id)).slice(
    0,
    DEFAULT_EXTRA_LIMIT,
  );
  const others = groups
    .filter((group) => group.key !== owner.key)
    .map((group) => fresh(group.models.map((model) => model.id))[0])
    .filter((id): id is string => Boolean(id));
  return [...own, ...others];
}

/** A per-million price at the two decimals a reader can compare, or more when
 *  two decimals would round it away. Reseller rates arrive with a repeating
 *  tail (`$1.1176470588`), which is noise in a one-line row. */
const money = (value: number): string => {
  const rounded = Math.round(value * 100) / 100;
  if (rounded > 0) return `$${rounded}`;
  return `$${Number(value.toFixed(4))}`;
};

/** Price as the rows print it: input and output per million tokens. A
 *  agent-lane-only model may carry its default cheap rate at the top level, so
 *  an absent direct lane must stay blank rather than borrowing that rate. */
export const priceLabel = (
  model: PickerModel,
  lane: Lane = 'direct',
): string => {
  let input = model.inputPerMtok;
  let output = model.outputPerMtok;
  if (
    lane === 'direct' &&
    model.lanes?.length &&
    !model.lanes.some((entry) => entry.key === 'direct')
  ) {
    return '';
  }
  if (lane !== 'direct') {
    const row = (model.lanes || []).find((entry) => entry.key === lane);
    if (!row) return '';
    input = row.inputPerMtok;
    output = row.outputPerMtok;
  }
  if (input === undefined) return '';
  const tail = output === undefined ? '' : ` / ${money(output)}`;
  return `${money(input)}${tail}`;
};

/** The single client a tool speaks as, when a lane can insist on one. Only
 *  Codex-only sources exist today; anything else is left unrestricted rather
 *  than guessed at. */
const TOOL_CLIENT: Partial<Record<ConnectToolId, string>> = {
  'claude-code': 'claude_code',
  codex: 'codex',
};

/** What a model's lane control may offer for one tool.
 *
 *  - `choice`: both lanes are real here, so the control is live.
 *  - `client-locked`: the agent lane routes, but only for another client. Its
 *    price still shows — the saving is real, just not from this tool.
 *  - `agent-lane-only`: only the agent lane has a price and a usable source.
 *  - `direct-only`: the agent lane was never priced, or has nothing serving
 *    it. No control at all, one line of prose instead. */
export type LaneOffer = {
  cheapPrice: string;
  directPrice: string;
  kind: 'choice' | 'client-locked' | 'direct-only' | 'agent-lane-only';
  /** The client the agent lane insists on, for `client-locked`. */
  lockedTo?: string;
};

export const laneOffer = (
  model: PickerModel | undefined,
  toolId: ConnectToolId,
): LaneOffer => {
  const directPrice = model ? priceLabel(model, 'direct') : '';
  const cheapPrice = model ? priceLabel(model, 'cheap') : '';
  const cheap = (model?.lanes || []).find((entry) => entry.key === 'cheap');
  if (!model || !cheap || !cheap.available || !cheapPrice) {
    return { cheapPrice, directPrice, kind: 'direct-only' };
  }
  if (cheap.clientOnly && cheap.clientOnly !== TOOL_CLIENT[toolId]) {
    return {
      cheapPrice,
      directPrice,
      kind: 'client-locked',
      lockedTo: cheap.clientOnly,
    };
  }
  if (!directPrice) {
    return { cheapPrice, directPrice, kind: 'agent-lane-only' };
  }
  return { cheapPrice, directPrice, kind: 'choice' };
};

/** Release date as the rows print it, or an empty string when the catalog
 *  never recorded one. */
export const releaseLabel = (model: PickerModel): string => {
  if (!model.releasedAt) return '';
  const date = new Date(model.releasedAt * 1000);
  if (Number.isNaN(date.getTime())) return '';
  const month = String(date.getUTCMonth() + 1).padStart(2, '0');
  const day = String(date.getUTCDate()).padStart(2, '0');
  return `${date.getUTCFullYear()}-${month}-${day}`;
};

export const findPickerModel = (
  result: PickerResult,
  id: string,
): PickerModel | undefined => {
  for (const section of result.sections) {
    for (const group of section.groups) {
      for (const model of group.models) {
        if (model.id === id) return model;
        const variant = model.variants.find((item) => item.id === id);
        if (variant) return variant;
      }
    }
  }
  return undefined;
};

/** The vendor group holding an id, variants included. */
export const groupKeyOf = (
  result: PickerResult,
  id: string,
): string | undefined => {
  for (const section of result.sections) {
    for (const group of section.groups) {
      const hit = group.models.some(
        (model) =>
          model.id === id ||
          model.variants.some((variant) => variant.id === id),
      );
      if (hit) return group.key;
    }
  }
  return undefined;
};

/** The group the field must expand to put `id` on screen, or undefined when
 *  the resting list already shows it. A model past its vendor's first rows,
 *  or folded under another as a variant, would otherwise be missing from the
 *  open list — and the first visible row, not the current choice, is what the
 *  keyboard would be sitting on. */
export const groupToRevealFor = (
  result: PickerResult,
  id: string,
  visible: number,
): string | undefined => {
  if (!id) return undefined;
  for (const section of result.sections) {
    for (const group of section.groups) {
      const index = group.models.findIndex((model) => model.id === id);
      if (index >= 0) return index < visible ? undefined : group.key;
      const holdsVariant = group.models.some((model) =>
        model.variants.some((variant) => variant.id === id),
      );
      if (holdsVariant) return group.key;
    }
  }
  return undefined;
};

/** A tool is only offered models it can reach; `toolEndpointLabel` names those
 *  protocols for the empty state. A tool with no requirement of its own speaks
 *  OpenAI chat, which is what the message should say rather than nothing. */
export const toolEndpointLabel = (tool: ConnectTool): string => {
  const required = TOOL_REQUIRED_ENDPOINTS[tool.id];
  return required && required.length ? required.join(' / ') : 'openai';
};

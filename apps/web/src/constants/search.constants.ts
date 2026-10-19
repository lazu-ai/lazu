// Search / retrieval backend providers exposed by Lazu's `/v1/search`.
//
// The `id` here is exactly the value sent as the request `backend` field
// (or the special `auto`, which lets the control plane pick). The console
// uses this list as a static fallback for the provider dropdown when the
// live `/api/search/backends` lookup is unavailable.
//
// NOTE (backend coordination): `channelType` numeric IDs must match the
// search-backend channel types the API server defines. The model-provider
// channel types currently in use are 1 / 8 / 14 / 24 / 48 (see
// channel.constants.ts), so search backends are allocated from 60+.

export type SearchCapability = 'web_search' | 'web_fetch' | 'answer';

export type SearchProvider = {
  /** Value sent as the `backend` request field, e.g. `tavily`. */
  id: string;
  /** Display name (proper noun, not translated). */
  label: string;
  capabilities: SearchCapability[];
  /** Short price hint shown in the picker; provider list overrides it when live. */
  priceHint?: string;
  /** Maps to the channel `type` used by the 检索后端 admin page. */
  channelType: number;
};

/** Special backend value: let the control plane route by health / cost / priority. */
export const SEARCH_AUTO_BACKEND = 'auto';

export const SEARCH_PROVIDERS: SearchProvider[] = [
  {
    id: 'tavily',
    label: 'Tavily',
    capabilities: ['web_search', 'web_fetch'],
    priceHint: '$8 / 1k',
    channelType: 60,
  },
  {
    id: 'exa',
    label: 'Exa',
    capabilities: ['web_search'],
    priceHint: 'cost 回传',
    channelType: 61,
  },
  {
    id: 'jina',
    label: 'Jina',
    capabilities: ['web_search', 'web_fetch'],
    priceHint: '$0.02 / 1M',
    channelType: 62,
  },
  {
    id: 'serper',
    label: 'Serper',
    capabilities: ['web_search'],
    priceHint: '$1 / 1k',
    channelType: 63,
  },
  {
    id: 'brave',
    label: 'Brave',
    capabilities: ['web_search'],
    priceHint: '$5 / 1k',
    channelType: 64,
  },
];

/** Channel-type options used by the dedicated 检索后端 (search backend) admin page. */
export const SEARCH_CHANNEL_OPTIONS = SEARCH_PROVIDERS.map((provider) => ({
  value: provider.channelType,
  label: provider.label,
  provider: provider.id,
  capabilities: provider.capabilities,
}));

export const SEARCH_CHANNEL_TYPES = SEARCH_PROVIDERS.map(
  (provider) => provider.channelType,
);

export const getSearchProviderByChannelType = (
  type: number | undefined,
): SearchProvider | undefined =>
  SEARCH_PROVIDERS.find((provider) => provider.channelType === type);

/** `search_depth` values accepted by `/v1/search`. */
export const SEARCH_DEPTH_OPTIONS = ['basic', 'advanced'] as const;
export type SearchDepth = (typeof SEARCH_DEPTH_OPTIONS)[number];

/** Normalized capability labels for chips. Provider proper-nouns stay untranslated. */
export const SEARCH_CAPABILITY_LABEL: Record<SearchCapability, string> = {
  web_search: 'web_search',
  web_fetch: 'web_fetch',
  answer: 'answer',
};

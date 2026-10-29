// Docs live under the main domain (lazu.ai/docs) so their links build the
// same site's authority. A self-hosted build can point elsewhere.
const DEFAULT_DOCS_URL = 'https://lazu.ai/docs';

export const getDocsBaseURL = () =>
  String(import.meta.env.VITE_BRAND_DOCS_URL || DEFAULT_DOCS_URL).replace(
    /\/+$/,
    '',
  );

export const getDocsURL = (path = '') => {
  const normalizedPath = path ? `/${path.replace(/^\/+/, '')}` : '';
  return `${getDocsBaseURL()}${normalizedPath}`;
};

/** Docs locales are a subset of app locales; anything else falls back to en. */
const DOCS_LOCALES = new Set(['ja', 'zh', 'zh-TW']);

export const getDocsQuickstartURL = (language = '') => {
  const locale = DOCS_LOCALES.has(language) ? `/${language}` : '';
  return getDocsURL(`${locale}/quickstart`);
};

/** Docs home for a UI locale: /docs, /docs/zh/, /docs/zh-TW/, /docs/ja/.
 *  Locale homes are canonical with their slash; the slashless spelling
 *  redirects (apps/docs/nginx.conf). */
export const getDocsHomeURL = (language = '') =>
  DOCS_LOCALES.has(language) ? getDocsURL(`${language}/`) : getDocsBaseURL();

/* Where readers write for help, and the community they can join. Both are
 * deployment facts read at build time, like the docs URL above, because the
 * public pages are prerendered and cannot wait for the server to say. A
 * build without VITE_BRAND_COMMUNITY_URL shows no community link at all. */
const DEFAULT_SUPPORT_EMAIL = 'support@lazu.ai';

export const getSupportEmail = () =>
  String(
    import.meta.env.VITE_BRAND_SUPPORT_EMAIL || DEFAULT_SUPPORT_EMAIL,
  ).trim();

/** The community link (a Telegram group, a forum …), or null when the build
 *  names none. Only absolute http(s) URLs are accepted. */
export const getCommunityURL = (): string | null => {
  const raw = String(import.meta.env.VITE_BRAND_COMMUNITY_URL || '').trim();
  return /^https?:\/\/\S+$/.test(raw) ? raw : null;
};

/* The chat app a community link opens, when its host says so, so an entry
 * can be labelled with the place it leads ("Telegram") rather than a generic
 * word. Any other host (a forum, a self-hosted chat) gets null and the
 * caller's generic label. */
const COMMUNITY_PLATFORMS: { hosts: string[]; name: string }[] = [
  { hosts: ['t.me', 'telegram.me', 'telegram.dog'], name: 'Telegram' },
  { hosts: ['discord.gg', 'discord.com'], name: 'Discord' },
];

export const getCommunityPlatform = (url: string | null): string | null => {
  if (!url) return null;
  let host: string;
  try {
    host = new URL(url).hostname.toLowerCase().replace(/^www\./, '');
  } catch {
    return null;
  }
  return (
    COMMUNITY_PLATFORMS.find((platform) => platform.hosts.includes(host))
      ?.name ?? null
  );
};

/* The repository behind the GitHub entry on the public pages. Same order as
 * the docs URL: a build may point at a fork (VITE_BRAND_SOURCE_URL), the
 * running deployment may name where its own source is published
 * (`site.source_url`, the AGPL link the console also shows), and the upstream
 * project is the floor, so the prerendered pages always carry an entry. */
const DEFAULT_SOURCE_URL = 'https://github.com/lazu-ai/lazu';

export const getSourceURL = (deploymentSourceURL?: string | null): string => {
  const candidates = [
    import.meta.env.VITE_BRAND_SOURCE_URL,
    deploymentSourceURL,
    DEFAULT_SOURCE_URL,
  ];
  for (const raw of candidates) {
    const value = String(raw || '')
      .trim()
      .replace(/\/+$/, '');
    if (/^https?:\/\/\S+$/.test(value)) return value;
  }
  return DEFAULT_SOURCE_URL;
};

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

/** A docs page in the reader's language: /docs/<page> for English,
 *  /docs/<locale>/<page> for the docs locales. */
export const getDocsPageURL = (page: string, language = '') => {
  const locale = DOCS_LOCALES.has(language) ? `/${language}` : '';
  return getDocsURL(`${locale}/${page.replace(/^\/+/, '')}`);
};

export const getDocsQuickstartURL = (language = '') =>
  getDocsPageURL('quickstart', language);

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

/* Model pages the public footer lists under "Popular models". Which models
 * are popular is a fact about one deployment's traffic and catalog, so the
 * build names them (VITE_BRAND_FOOTER_MODELS) and a build without the list
 * shows no such column. The value is comma-separated `<model page>=<label>`
 * pairs, where the page is the path under /models, e.g.
 * `anthropic/claude-opus-5-5=Claude Opus 5.5`. Malformed entries are dropped. */
export type FooterModel = { path: string; label: string };

const FOOTER_MODEL_PATH = /^[A-Za-z0-9._-]+(?:\/[A-Za-z0-9._-]+)+$/;

export const getFooterModels = (): FooterModel[] => {
  const raw = String(import.meta.env.VITE_BRAND_FOOTER_MODELS || '');
  const models: FooterModel[] = [];
  for (const entry of raw.split(',')) {
    const separator = entry.indexOf('=');
    if (separator < 0) continue;
    const path = entry.slice(0, separator).trim().replace(/^\/+/, '');
    const label = entry.slice(separator + 1).trim();
    if (!label || !FOOTER_MODEL_PATH.test(path)) continue;
    if (path.split('/').some((segment) => /^\.+$/.test(segment))) continue;
    models.push({ path: `/models/${path}`, label });
  }
  return models;
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

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

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

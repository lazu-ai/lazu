// URL locale handling for the marketing surface.
//
// English lives unprefixed (/, /faq, /privacy, /terms); zh, zh-TW and ja get
// real URL prefixes (/zh/, /zh/faq, /zh/privacy, …) so the
// three non-English versions are
// crawlable at all — until these routes existed the translations rendered
// only client-side via i18next and no crawler ever saw them. The same
// bare/prefixed mapping is used by the router, the language switcher, the
// prerender scripts and the hydration guard, so it lives in one module.

export type MarketingLocale = 'en' | 'ja' | 'zh' | 'zh-TW';

export const MARKETING_BARE_PATHS = [
  '/',
  '/faq',
  '/about',
  '/privacy',
  '/terms',
] as const;

/** Locales that carry a URL prefix. English is the unprefixed default. */
export const PREFIXED_MARKETING_LOCALES = ['zh', 'zh-TW', 'ja'] as const;

// Longest prefix first so /zh-TW never matches as /zh + "-TW".
const PREFIX_PATTERN = /^\/(zh-TW|zh|ja)(?=\/|$)/;

export type SplitMarketingPath = {
  /** The path with the locale prefix removed ('/', '/faq', …). */
  barePath: string;
  locale: MarketingLocale;
};

export const splitMarketingPath = (pathname: string): SplitMarketingPath => {
  const match = PREFIX_PATTERN.exec(pathname || '/');
  if (!match) return { barePath: pathname || '/', locale: 'en' };
  const rest = pathname.slice(match[0].length) || '/';
  return { barePath: rest, locale: match[1] as MarketingLocale };
};

export const isMarketingBarePath = (barePath: string): boolean =>
  (MARKETING_BARE_PATHS as readonly string[]).includes(
    barePath.replace(/\/+$/, '') || '/',
  );

/** '/faq' + 'zh' -> '/zh/faq'; '/' + 'zh' -> '/zh/'; anything + 'en' -> bare. */
export const localizedMarketingPath = (
  barePath: string,
  locale: MarketingLocale,
): string => {
  const bare = barePath === '' ? '/' : barePath;
  if (locale === 'en') return bare;
  return bare === '/' ? `/${locale}/` : `/${locale}${bare}`;
};

/** The locale a marketing URL asserts, or null when the path is unprefixed. */
export const localeFromPath = (pathname: string): MarketingLocale | null => {
  const match = PREFIX_PATTERN.exec(pathname || '/');
  return match ? (match[1] as MarketingLocale) : null;
};

import i18n from 'i18next';
import LanguageDetector from 'i18next-browser-languagedetector';
import { initReactI18next } from 'react-i18next';
import { isMarketingStartupPath } from './marketing-startup';
import { extensionI18n, surfaceLanguageKey } from '../extensions/i18n';
import type { BackendModule, ReadCallback } from 'i18next';

type LocaleCode = 'en' | 'zh' | 'zh-TW' | 'ja';
type Namespace = string;
type TranslationResource = Record<string, unknown>;

// One compressed chunk per namespace contains the locale and its fallbacks.
const coreResourceLoaders: Record<
  Namespace,
  Record<LocaleCode, () => Promise<unknown>>
> = {
  translation: {
    en: () => import('virtual:lazu-locale/translation/en'),
    zh: () => import('virtual:lazu-locale/translation/zh'),
    'zh-TW': () => import('virtual:lazu-locale/translation/zh-TW'),
    ja: () => import('virtual:lazu-locale/translation/ja'),
  },
  marketing: {
    en: () => import('virtual:lazu-locale/marketing/en'),
    zh: () => import('virtual:lazu-locale/marketing/zh'),
    'zh-TW': () => import('virtual:lazu-locale/marketing/zh-TW'),
    ja: () => import('virtual:lazu-locale/marketing/ja'),
  },
  projects: {
    en: () => import('virtual:lazu-locale/projects/en'),
    zh: () => import('virtual:lazu-locale/projects/zh'),
    'zh-TW': () => import('virtual:lazu-locale/projects/zh-TW'),
    ja: () => import('virtual:lazu-locale/projects/ja'),
  },
  errors: {
    en: () => import('virtual:lazu-locale/errors/en'),
    zh: () => import('virtual:lazu-locale/errors/zh'),
    'zh-TW': () => import('virtual:lazu-locale/errors/zh-TW'),
    ja: () => import('virtual:lazu-locale/errors/ja'),
  },
};

const resourceLoaders = Object.assign(
  {},
  ...extensionI18n.map((extension) => extension.localeLoaders ?? {}),
  coreResourceLoaders,
) as Record<Namespace, Record<LocaleCode, () => Promise<unknown>>>;

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null;

const isLocaleCode = (language: string): language is LocaleCode =>
  Object.prototype.hasOwnProperty.call(resourceLoaders.translation, language);

const isNamespace = (namespace: string): namespace is Namespace =>
  Object.prototype.hasOwnProperty.call(resourceLoaders, namespace);

const toTranslationResource = (module: unknown): TranslationResource => {
  const payload =
    isRecord(module) && 'default' in module ? module.default : module;
  if (isRecord(payload) && isRecord(payload.translation)) {
    return payload.translation as TranslationResource;
  }
  if (isRecord(payload)) {
    return payload as TranslationResource;
  }
  return {};
};

// Browsers in zh-CN / zh-Hans config that are physically in
// Taiwan / HK / Macau time zones almost certainly want Traditional
// Chinese — without this hint we'd hand them simplified by default.
// Time zone is preferred over IP geolocation: client-side, no PII
// leaves the browser, and VPN-immune (a Beijing user on a US VPN
// still reports Asia/Shanghai). It's only consulted when the
// detected language is the ambiguous-zh variant AND nothing has been
// stored to localStorage yet (i.e. genuinely a first-time visit).
const HANT_TIMEZONES = /^Asia\/(Taipei|Hong_Kong|Macau)$/i;
const AMBIGUOUS_ZH = /^zh($|-cn|-hans)/i;
// i18next-browser-languagedetector uses different default keys per
// storage backend: localStorage / sessionStorage default to
// `i18nextLng`, but the cookie cache defaults to `i18next`. We read
// both so storage-restricted clients (Safari ITP, private browsing,
// IT-policy locked-down browsers) where only the cookie is available
// don't get treated as "no preference set".
const STORAGE_KEY = 'i18nextLng';
const COOKIE_KEY = 'i18next';

const detectsHantTimezone = (): boolean => {
  try {
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || '';
    return HANT_TIMEZONES.test(tz);
  } catch {
    return false;
  }
};

// Strict variant mapping — never overrides a value the user has
// chosen. Used by the backend loader and by convertDetectedLanguage
// once the user has a stored preference.
const normalizeLanguage = (raw: unknown): string => {
  if (!raw) return 'en';
  const lower = String(raw).replace(/_/g, '-');
  if (/^zh[-]?(tw|hk|hant|mo)/i.test(lower)) return 'zh-TW';
  if (AMBIGUOUS_ZH.test(lower)) return 'zh';
  return lower.split('-')[0].toLowerCase();
};

// Read the persisted i18next language preference from any source the
// browser-language-detector might have written to. Privacy modes /
// enterprise IT policies / Safari ITP can block localStorage while
// cookies still work — checking only one source means we'd
// false-negative those users and re-run the timezone tiebreaker on
// every reload, silently reverting their explicit choice.
const readCookie = (name: string): string | null => {
  try {
    if (typeof document === 'undefined' || !document.cookie) return null;
    const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    const match = document.cookie.match(
      new RegExp('(?:^|;\\s*)' + escaped + '=([^;]*)'),
    );
    return match ? decodeURIComponent(match[1]) : null;
  } catch {
    return null;
  }
};

const readStoredPreference = (): string | null => {
  try {
    const local = localStorage.getItem(STORAGE_KEY);
    if (local) return local;
  } catch {}
  // Cookie default key is `i18next`; also check the localStorage key as
  // a defensive fallback in case the detector was ever configured with
  // a unified key in the future.
  return readCookie(COOKIE_KEY) || readCookie(STORAGE_KEY);
};

// Apply timezone tiebreaker ONLY for the first-visit path (no stored
// preference yet). Without this gate, a user who explicitly picks
// 简体中文 from the language switcher in Taiwan would have their
// stored `zh` flipped back to `zh-TW` on every reload — silently
// reverting their explicit choice.
const convertDetectedLanguage = (raw: unknown): string => {
  // querystring / cookie / Java-style locales can ship `zh_CN` / `zh_Hans`
  // with an underscore separator. Normalize first so the ambiguity regex
  // matches every variant the same way `normalizeLanguage` does.
  const dashed = String(raw || '').replace(/_/g, '-');
  if (
    !readStoredPreference() &&
    AMBIGUOUS_ZH.test(dashed) &&
    detectsHantTimezone()
  ) {
    return 'zh-TW';
  }
  return normalizeLanguage(raw);
};

let loadingLanguage = '';
i18n.on('languageChanging', (language) => {
  loadingLanguage = language;
});

const dynamicLocaleBackend: BackendModule = {
  type: 'backend',
  init() {},
  read(language: string, namespace: string, callback: ReadCallback) {
    const normalized = normalizeLanguage(language);
    const normalizedNamespace = isNamespace(namespace)
      ? namespace
      : 'translation';
    const namespaceLoaders = resourceLoaders[normalizedNamespace];
    // i18next requests fallback languages concurrently. They share the same
    // import, while retaining separate resource stores and plural rules.
    const target = normalizeLanguage(
      loadingLanguage || i18n.language || language,
    );
    const chain = target === 'zh-TW' ? ['zh-TW', 'zh', 'en'] : [target, 'en'];
    const bundleLanguage = chain.includes(normalized) ? target : normalized;
    const loadLocale =
      namespaceLoaders[isLocaleCode(bundleLanguage) ? bundleLanguage : 'en'];

    loadLocale()
      .then((module) => {
        const resources = toTranslationResource(module);
        callback(
          null,
          (resources[normalized] || resources.en || {}) as TranslationResource,
        );
      })
      .catch((error: unknown) => {
        callback(error instanceof Error ? error : String(error), false);
      });
  },
};

// A locale-prefixed marketing URL (/zh/, /zh-TW/faq, /ja/faq) asserts its
// language in the address bar — it must outrank any stored preference,
// otherwise the client would hydrate /zh/ with an English tree against the
// Chinese prerender. Only these three prefixes exist; every other path
// (console, auth, bare marketing) keeps normal detection.
const pathAssertedLanguage = (): string | undefined => {
  try {
    if (typeof window === 'undefined') return undefined;
    const match = /^\/(zh-TW|zh|ja)(?=\/|$)/.exec(window.location.pathname);
    return match ? match[1] : undefined;
  } catch {
    return undefined;
  }
};

// An extension surface's signed-in screens carry no language in their
// address. They follow the language of the surface's public page the visitor
// came from (stored under the surface's key), so a visitor reading it in
// English is not dropped into Chinese because the console once stored zh. On
// these screens nothing is written back to the console's own preference.
const surfaceStorageKey = (): string | undefined => {
  try {
    return typeof window === 'undefined'
      ? undefined
      : surfaceLanguageKey(window.location.pathname);
  } catch {
    return undefined;
  }
};

const surfaceLanguage = (): string | undefined => {
  const key = surfaceStorageKey();
  if (!key) return undefined;
  try {
    const stored = window.localStorage.getItem(key) || '';
    return ['en', 'zh', 'zh-TW', 'ja'].includes(stored) ? stored : undefined;
  } catch {
    return undefined;
  }
};

export const i18nReady = i18n
  .use(LanguageDetector)
  .use(dynamicLocaleBackend)
  .use(initReactI18next)
  .init({
    lng: pathAssertedLanguage() || surfaceLanguage(),
    load: 'currentOnly',
    // `errors` (failure copy) is not needed to draw the first screen; it
    // loads in the background once the first screen's namespaces are in
    // (loadErrorsNamespace below).
    ns: [
      'translation',
      ...(typeof window === 'undefined' ||
      isMarketingStartupPath(window.location.pathname)
        ? ['marketing']
        : []),
    ],
    defaultNS: 'translation',
    fallbackLng: {
      'zh-TW': ['zh', 'en'],
      zh: ['en'],
      ja: ['en'],
      default: ['en'],
    },
    supportedLngs: ['zh', 'zh-TW', 'en', 'ja'],
    nonExplicitSupportedLngs: false,
    // Map browser/storage variants (zh-HK, zh-Hant, zh-MO, en-US, …)
    // BEFORE i18next checks supportedLngs — otherwise zh-HK users would
    // fail the supportedLngs gate and fall back to `en` instead of
    // resolving to `zh-TW`.
    detection: {
      convertDetectedLanguage,
      ...(surfaceStorageKey() ? { caches: [] } : {}),
    },
    react: {
      useSuspense: true,
    },
    interpolation: {
      escapeValue: false,
    },
  });

/* The document says which language its text is in. The prerendered locale
   pages set <html lang> at build time, but the client switches language
   after load (stored preference, the language menu), and without this the
   page kept claiming lang="en" over Chinese copy: screen readers read it in
   the wrong voice and every :lang(zh) rule — CJK emphasis without italics,
   keep-all line breaking — never applied. */
const syncDocumentLanguage = (language?: string) => {
  if (typeof document === 'undefined' || !language) return;
  if (document.documentElement.lang !== language) {
    document.documentElement.lang = language;
  }
};
i18n.on('languageChanged', syncDocumentLanguage);

let errorsNamespace: Promise<void> | undefined;

/** Whether the failure copy can be read synchronously right now. */
export const errorsNamespaceReady = (): boolean =>
  i18n.isInitialized && i18n.hasLoadedNamespace('errors');

/** Loads the `errors` namespace once; later calls share the same promise.
 *  A failed load (offline) resolves too, so callers fall back to their own
 *  copy instead of waiting forever, and the next call tries again. */
export const loadErrorsNamespace = (): Promise<void> => {
  if (errorsNamespaceReady()) return Promise.resolve();
  errorsNamespace ??= i18nReady
    .then(() => i18n.loadNamespaces('errors'))
    .catch(() => undefined)
    .finally(() => {
      errorsNamespace = undefined;
    });
  return errorsNamespace;
};

// Off the first screen's critical path, but early: most failures are shown
// long after this resolves. API failures and showError() wait for it anyway.
if (typeof window !== 'undefined') void loadErrorsNamespace();
void i18nReady.then(() => syncDocumentLanguage(i18n.resolvedLanguage));

export default i18n;

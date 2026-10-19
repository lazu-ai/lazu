/* The part of an extension the i18n setup needs, kept apart from its React
   code so the language loader can start before anything else. Builds with
   extensions place it at src/extensions/<name>/i18n.ts. */

export interface ExtensionI18n {
  /* Locale namespaces the extension loads: namespace → language → chunk. */
  localeLoaders?: Record<string, Record<string, () => Promise<unknown>>>;
  /* Surfaces whose signed-in screens follow the language their public pages
     were read in, stored under storageKey, instead of the console's. */
  surfaceLanguages?: { prefix: string; storageKey: string }[];
  /* Clients the extension sends traffic as (User-Agent framework), shown as
     a monogram badge in usage views. */
  clientBadges?: Record<string, { mark: string; name: string }>;
}

// import.meta.glob exists only under Vite; Node (Playwright helpers that
// import app modules) sees no extensions.
let modules: Record<string, { default: ExtensionI18n }> = {};
try {
  modules = import.meta.glob<{ default: ExtensionI18n }>('./*/i18n.ts', {
    eager: true,
  });
} catch {
  modules = {};
}

export const extensionI18n: readonly ExtensionI18n[] = Object.keys(modules)
  .sort()
  .map((key) => modules[key].default);

/* The storage key of the surface language for pathname, if any. */
export const surfaceLanguageKey = (pathname: string): string | undefined =>
  extensionI18n
    .flatMap((extension) => extension.surfaceLanguages ?? [])
    .find(
      (surface) =>
        pathname === surface.prefix ||
        pathname.startsWith(`${surface.prefix}/`),
    )?.storageKey;

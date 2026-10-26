import type {
  ExtensionSurface,
  OpsTab,
  SidebarEntry,
  TopUpOffer,
  WebExtension,
} from './types';

export type {
  ExtensionSurface,
  LocalizedLabel,
  OpsTab,
  OpsTabContext,
  PublicNav,
  SidebarEntry,
  StatusPayload,
  TopUpOffer,
  WebExtension,
} from './types';

/* Extensions compiled into this build: src/extensions/<name>/extension.tsx,
   each exporting its WebExtension as default. None in the public tree.

   Everything here is read on first use, never while modules are loading: an
   extension imports core modules that import this one, so a module-level read
   would see the list before the extension finished loading. Call these from
   components, hooks and handlers, not at the top of a module. */
// import.meta.glob exists only under Vite; Node (Playwright helpers that
// import app modules) sees no extensions.
let modules: Record<string, { default: WebExtension }> = {};
try {
  modules = import.meta.glob<{ default: WebExtension }>('./*/extension.tsx', {
    eager: true,
  });
} catch {
  modules = {};
}

let loaded: readonly WebExtension[] | undefined;

export const webExtensions = (): readonly WebExtension[] =>
  (loaded ??= Object.keys(modules)
    .sort()
    .map((key) => modules[key].default));

export const extensionSurfaces = (): readonly ExtensionSurface[] =>
  webExtensions().flatMap((extension) => extension.surfaces ?? []);

export const extensionOpsTabs = (): readonly OpsTab[] =>
  webExtensions().flatMap((extension) => extension.opsTabs ?? []);

export const extensionSidebarEntries = (): readonly SidebarEntry[] =>
  webExtensions().flatMap((extension) => extension.sidebar ?? []);

/* The surface whose prefix contains pathname, if any. */
export const surfaceForPath = (
  pathname: string,
): ExtensionSurface | undefined =>
  extensionSurfaces().find(
    (surface) =>
      pathname === surface.prefix || pathname.startsWith(`${surface.prefix}/`),
  );

/* The first offer any extension makes for the next top-up. Extension hooks
   run unconditionally and in a fixed order, as hooks must. */
export function useTopUpOffer(enabled: boolean): TopUpOffer {
  let offer: TopUpOffer | null = null;
  for (const extension of webExtensions()) {
    const next = extension.useTopUpOffer?.(enabled) ?? null;
    offer = offer ?? next;
  }
  return offer ?? { note: null };
}

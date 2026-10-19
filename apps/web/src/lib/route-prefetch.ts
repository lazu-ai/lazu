/* Warm a route on pointer or keyboard intent. Its module and eligible data
 * requests begin together; the mounted page reuses the same query cache.
 * Keep these import paths aligned with App.tsx's lazy routes. */
import { useEffect } from 'react';
import { prefetchAuthStatus } from '../components/auth/authStatus';
import { prefetchLoginSession } from '../components/auth/loginSession';
import { surfaceForPath } from '../extensions';

type ChunkLoader = () => Promise<unknown>;

/* Exact paths first, prefixes second: /console must not claim /console/token. */
const EXACT: Record<string, ChunkLoader> = {
  '/console': () => import('../pages/Dashboard'),
  '/console/health': () => import('../pages/ChannelHealth'),
  '/console/models': () => import('../pages/Routes'),
  '/console/organizations': () => import('../pages/Organizations'),
  '/console/personal': () => import('../components/settings/PersonalSetting'),
  '/console/setting': () => import('../pages/Setting'),
  '/console/token': () => import('../pages/Token'),
  '/console/usage': () => import('../pages/Usage'),
  '/console/wallet': () => import('../pages/Wallet'),
  '/console/model-ops': () => import('../pages/ModelOps'),
  '/console/model-ops/sources': () => import('../pages/ModelOps/SourcesPage'),
  '/console/price-plans': () => import('../pages/ModelOps/PricePlansPage'),
  '/console/ops': () => import('../pages/Console/Ops'),
  '/console/ops/mail': () => import('../pages/Console/Ops'),
  '/console/project/members': () =>
    import('../features/projects/pages/MembersPage'),
  '/console/project/settings': () =>
    import('../features/projects/pages/SettingsPage'),
  '/key': () => import('../pages/KeyInspector'),
  '/login': () => import('../components/auth/LoginForm'),
  '/reset': () => import('../components/auth/PasswordResetForm'),
};

const PREFIXED: Array<[string, ChunkLoader]> = [
  ['/console/ops/mail/', () => import('../pages/Console/Ops')],
  ['/invite/', () => import('../features/projects/pages/InviteAcceptPage')],
];

/* A chunk is fetched at most once; the module cache does the deduping, and the
 * set keeps us from calling into it on every pointer move. */
const started = new Set<string>();

/** Warm whatever a click on `pathname` would need. Safe to call repeatedly. */
export function prefetchRoute(pathname: string) {
  const loader =
    EXACT[pathname] ??
    PREFIXED.find(([prefix]) => pathname.startsWith(prefix))?.[1] ??
    surfaceForPath(pathname)?.preload;
  if (!loader) return;

  // The door needs one answer as well as its code, and asking for it here is
  // what keeps the sign-in screen from painting a guess. Repeat this check on
  // later intents: the stored identity may have changed since the first hover.
  if (pathname === '/login') {
    prefetchAuthStatus();
    prefetchLoginSession();
  }

  if (pathname.startsWith('/console')) {
    void import('./console-prefetch')
      .then(({ prefetchConsoleData }) => prefetchConsoleData(pathname))
      .catch(() => undefined);
  }

  if (started.has(pathname)) return;
  started.add(pathname);
  void loader().catch(() => {
    started.delete(pathname);
  });
}

const internalPathOf = (target: EventTarget | null): string | null => {
  if (!(target instanceof Element)) return null;
  const href = target.closest('a[href^="/"]')?.getAttribute('href');
  if (!href || href.startsWith('//')) return null;
  return href.split(/[?#]/)[0] || null;
};

/* One delegated listener rather than a prop on every link: the sidebar, the
 * landing page, both headers and the FAQ all link into the app, and a
 * link added later should not have to remember to opt in. */
export function useRoutePrefetch() {
  useEffect(() => {
    const warm = (event: Event) => {
      const path = internalPathOf(event.target);
      if (path) prefetchRoute(path);
    };

    // Hover is the pointer's intent; focus is the keyboard's.
    document.addEventListener('pointerover', warm);
    document.addEventListener('focusin', warm);
    return () => {
      document.removeEventListener('pointerover', warm);
      document.removeEventListener('focusin', warm);
    };
  }, []);
}

import { surfaceForPath } from '../../extensions';
export type ConsoleRouteMeta = {
  /** Chinese fallback for labels whose i18n key is not in the locale bundles
   *  yet. Render sites pass it as `defaultValue` so a new route never shows a
   *  raw key while the consolidated locale pass is pending. */
  fallback?: string;
  icon: string;
  label: string;
  match: string;
  /** A route whose name differs by audience carries the customer wording here.
   *  Render sites resolve it with `consoleRouteLabelKeys`, so the desktop
   *  sidebar, the mobile top bar and the route announcement always agree. */
  customerFallback?: string;
  customerLabel?: string;
};

/** Which i18n key and fallback a viewer should see for a route. Operators keep
 *  the operator wording; everyone else gets the customer wording when the route
 *  declares one. */
export const consoleRouteLabelKeys = (
  meta: ConsoleRouteMeta,
  isOperator: boolean,
): { fallback?: string; label: string } =>
  !isOperator && meta.customerLabel
    ? { fallback: meta.customerFallback, label: meta.customerLabel }
    : { fallback: meta.fallback, label: meta.label };

// Ordered most-specific first. This is the shared source for mobile page
// identity, route announcements, and nested-route active state.
export const CONSOLE_ROUTE_META: readonly ConsoleRouteMeta[] = [
  {
    match: '/console/search-backend',
    icon: 'search_backend',
    label: 'nav.searchBackend',
  },
  {
    match: '/console/project/members',
    icon: 'project_members',
    label: 'projects.nav.members',
    fallback: '成员与预算',
  },
  {
    match: '/console/project/settings',
    icon: 'project_settings',
    label: 'projects.nav.settings',
    fallback: '项目设置',
  },
  {
    match: '/console/organizations',
    icon: 'user',
    label: 'nav.sidebarOrganizations',
  },
  {
    match: '/console/price-plans',
    icon: 'user',
    label: 'page.console.modelOps.nav.pricePlans',
    fallback: '专属折扣',
  },
  {
    match: '/console/model-ops',
    icon: 'models',
    label: 'nav.modelOps',
    fallback: '模型运营',
  },
  {
    match: '/console/health',
    icon: 'channel_health',
    label: 'nav.modelStatus',
    fallback: '模型状态',
  },
  { match: '/console/setting', icon: 'setting', label: 'nav.settings' },
  {
    match: '/console/ops',
    icon: 'announcements',
    label: 'nav.ops',
    fallback: '运营',
  },
  { match: '/console/usage', icon: 'usage', label: 'nav.sidebarUsage' },
  {
    match: '/console/models',
    icon: 'model_market',
    label: 'nav.sidebarModels',
  },
  { match: '/console/key', icon: 'key_inspector', label: 'nav.keyInspector' },
  {
    match: '/console/wallet',
    icon: 'wallet',
    label: 'nav.wallet',
  },
  {
    match: '/console/personal',
    icon: 'personal',
    label: 'nav.personalSettings',
  },
  { match: '/console/token', icon: 'token', label: 'nav.apiKeys' },
  { match: '/console', icon: 'detail', label: 'nav.sidebarDashboard' },
];

/* Which sidebar group a console route belongs to. The top bar says where you
 * are as "<project> › <group>", so the group name has to come from the same
 * source the sidebar uses rather than from a second, drifting list. */
export type ConsoleSectionKey = 'workspace' | 'project' | 'account' | 'admin';

const SECTION_BY_PREFIX: ReadonlyArray<[string, ConsoleSectionKey]> = [
  ['/console/project', 'project'],
  ['/console/wallet', 'account'],
  ['/console/personal', 'account'],
  ['/console/organizations', 'admin'],
  ['/console/price-plans', 'admin'],
  ['/console/model-ops', 'admin'],
  ['/console/search-backend', 'admin'],
  ['/console/ops', 'admin'],
  ['/console/setting', 'admin'],
];

export const resolveConsoleSection = (pathname: string): ConsoleSectionKey =>
  SECTION_BY_PREFIX.find(([prefix]) => isPathWithin(pathname, prefix))?.[1] ??
  'workspace';

export const isPathWithin = (pathname: string, target: string) =>
  pathname === target || pathname.startsWith(`${target}/`);

export const isNavigationPathActive = (pathname: string, target: string) =>
  target === '/console' ? pathname === target : isPathWithin(pathname, target);

export const resolveConsoleRouteMeta = (pathname: string): ConsoleRouteMeta =>
  CONSOLE_ROUTE_META.find(({ match }) => isPathWithin(pathname, match)) ??
  CONSOLE_ROUTE_META[CONSOLE_ROUTE_META.length - 1];

export type RouteTransitionKind =
  | 'none'
  | 'same-level'
  | 'drill-forward'
  | 'drill-back'
  | 'cross-surface';

const routeSurface = (pathname: string) => {
  if (pathname.startsWith('/console')) return 'console';
  if (surfaceForPath(pathname)) return 'surface';
  if (pathname === '/' || pathname === '/faq' || pathname === '/about') {
    return 'public';
  }
  return 'app';
};

export const getRouteTransitionKind = (
  previousPathname: string | null,
  pathname: string,
  navigationType: 'POP' | 'PUSH' | 'REPLACE',
): RouteTransitionKind => {
  if (!previousPathname || previousPathname === pathname) return 'none';
  if (routeSurface(previousPathname) !== routeSurface(pathname)) {
    return 'cross-surface';
  }
  const isNestedTransition =
    previousPathname !== '/console' &&
    pathname !== '/console' &&
    (isPathWithin(pathname, previousPathname) ||
      isPathWithin(previousPathname, pathname));
  if (isNestedTransition) {
    return navigationType === 'POP' ? 'drill-back' : 'drill-forward';
  }
  return 'same-level';
};

export const isUsageLogsPath = (pathname: string, search: string) => {
  if (pathname !== '/console/usage') return false;
  const tab = new URLSearchParams(search).get('tab');
  return tab === null || tab === 'logs';
};

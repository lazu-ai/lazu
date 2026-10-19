/* "Did you mean …?" for the 404 page.
 *
 * Only real destinations are offered: the console routes the sidebar already
 * knows (CONSOLE_ROUTE_META) and a handful of public pages. A suggestion is
 * made when the address is one or two keystrokes away from one of them, or
 * when it runs past a real page into a sub-path that does not exist
 * (/console/token/abc → API Keys). Anything further away gets no hint at all:
 * a wrong guess is worse than none.
 */
import {
  CONSOLE_ROUTE_META,
  consoleRouteLabelKeys,
} from '../components/layout/routeMeta';

export type RouteSuggestion = {
  fallback?: string;
  label: string;
  path: string;
};

const PUBLIC_ROUTES: readonly RouteSuggestion[] = [
  { path: '/models', label: 'nav.models' },
  { path: '/login', label: 'legacy.i18n.sign_in.21f1e88275' },
  { path: '/privacy', label: 'legacy.i18n.privacy_policy.8c276c1fea' },
  { path: '/terms', label: 'legacy.i18n.user_agreement.0399ad7167' },
];

/* The route meta carries the mobile / announcement wording; where the
 * sidebar names a page differently, the suggestion uses the sidebar's name,
 * because that is the word the reader will look for next. */
const SIDEBAR_LABELS: Record<string, Omit<RouteSuggestion, 'path'>> = {
  '/console': { label: 'nav.sidebarDashboard', fallback: '总览' },
  '/console/models': { label: 'nav.sidebarModels', fallback: '模型' },
  '/console/usage': { label: 'nav.sidebarUsage', fallback: '用量与日志' },
  '/console/wallet': { label: 'nav.wallet', fallback: '钱包' },
  '/console/personal': { label: 'nav.personalSettings', fallback: '个人设置' },
  '/console/setting': { label: 'nav.sidebarSettings', fallback: '设置' },
};

const CANDIDATES: readonly RouteSuggestion[] = [
  ...CONSOLE_ROUTE_META.map((meta) => ({
    path: meta.match,
    ...(SIDEBAR_LABELS[meta.match] ?? consoleRouteLabelKeys(meta, false)),
  })),
  ...PUBLIC_ROUTES,
];

const MAX_DISTANCE = 2;

function distance(a: string, b: string): number {
  if (Math.abs(a.length - b.length) > MAX_DISTANCE) return MAX_DISTANCE + 1;
  let previous = Array.from({ length: b.length + 1 }, (_, index) => index);
  for (let i = 1; i <= a.length; i += 1) {
    const current = [i];
    for (let j = 1; j <= b.length; j += 1) {
      current[j] = Math.min(
        previous[j] + 1,
        current[j - 1] + 1,
        previous[j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1),
      );
    }
    previous = current;
  }
  return previous[b.length];
}

const normalise = (pathname: string) =>
  pathname.toLowerCase().replace(/\/+$/, '') || '/';

export function suggestRoute(pathname: string): RouteSuggestion | null {
  const path = normalise(pathname);
  let best: RouteSuggestion | null = null;
  let bestDistance = MAX_DISTANCE + 1;
  for (const candidate of CANDIDATES) {
    if (candidate.path === path) return null;
    const d = distance(path, candidate.path);
    if (d < bestDistance) {
      best = candidate;
      bestDistance = d;
    }
  }
  if (best) return best;
  // Longest real page the address runs past. /console itself is excluded:
  // every unknown console address runs past it, which says nothing.
  return (
    CANDIDATES.filter(
      (candidate) =>
        candidate.path !== '/console' && path.startsWith(`${candidate.path}/`),
    ).sort((a, b) => b.path.length - a.path.length)[0] ?? null
  );
}

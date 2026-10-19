import { isAdminRole } from './roles';

const DEFAULT_AUTH_DESTINATION = '/console';

const hasUnsafeURLCharacters = (value: string) =>
  value.includes('\\') || /[\u0000-\u001f\u007f]/.test(value);

/**
 * Accept only a same-origin absolute path for post-authentication navigation.
 * Browsers normalize backslashes and some control characters before resolving
 * URLs, so prefix checks alone are not sufficient to reject protocol-relative
 * destinations such as `/\\example.com`.
 */
export const safeInternalAuthPath = (
  value: string | null | undefined,
  currentOrigin?: string,
) => {
  if (
    !value ||
    !value.startsWith('/') ||
    value.startsWith('//') ||
    hasUnsafeURLCharacters(value)
  ) {
    return DEFAULT_AUTH_DESTINATION;
  }

  try {
    const candidateOrigin =
      currentOrigin ||
      (typeof window === 'undefined' ? '' : window.location.origin);
    if (!candidateOrigin) return DEFAULT_AUTH_DESTINATION;
    const origin = new URL(candidateOrigin).origin;
    const resolved = new URL(value, origin);
    if (
      resolved.origin !== origin ||
      !resolved.pathname.startsWith('/') ||
      resolved.pathname.startsWith('//')
    ) {
      return DEFAULT_AUTH_DESTINATION;
    }
    return `${resolved.pathname}${resolved.search}${resolved.hash}`;
  } catch {
    return DEFAULT_AUTH_DESTINATION;
  }
};

// Back-office destinations, including legacy aliases. Only 管理员 may land on
// them after sign-in; project roles never grant platform access, and route
// guards still authorize every visit.
const ADMIN_DESTINATIONS = [
  '/console/organizations',
  '/console/price-plans',
  '/console/user',
  '/console/model-ops',
  '/console/channel',
  '/console/search-backend',
  '/console/setting',
  '/console/announcements',
  '/console/ops',
];

export const postLoginDestination = (
  value: string | null | undefined,
  role: unknown,
  currentOrigin?: string,
) => {
  const destination = safeInternalAuthPath(value, currentOrigin);
  let pathname: string;
  try {
    pathname = decodeURIComponent(
      destination.split(/[?#]/, 1)[0],
    ).toLowerCase();
  } catch {
    return DEFAULT_AUTH_DESTINATION;
  }
  const within = (base: string) =>
    pathname === base || pathname.startsWith(`${base}/`);
  if (
    within('/login') ||
    within('/forbidden') ||
    within('/auth/oidc/callback')
  ) {
    return DEFAULT_AUTH_DESTINATION;
  }
  return ADMIN_DESTINATIONS.some(within) && !isAdminRole(role)
    ? DEFAULT_AUTH_DESTINATION
    : destination;
};

import { matchPath } from 'react-router-dom';
import { extensionSurfaces } from '../../extensions';

export const normalizeShellPath = (pathname: string) =>
  pathname.replace(/\/+$/, '').toLowerCase() || '/';

// Match the router's case-insensitive, trailing-slash-tolerant semantics.
// Invite accepts exactly one token; only routes with catch-alls own trees.
const APP_PATTERNS = [
  '/console/*',
  '/invite/:token',
  '/login',
  '/login/password',
  '/register',
  '/reset',
  '/quickstart',
  '/keyusage',
  '/user/reset',
  '/auth/oidc/callback',
  '/payment/success',
  '/payment/fail',
  '/forbidden',
];

export const isAppOwnedPath = (pathname: string) =>
  APP_PATTERNS.some((path) => matchPath({ path, end: true }, pathname)) ||
  extensionSurfaces().some((surface) =>
    matchPath({ path: `${surface.prefix}/*`, end: true }, pathname),
  );

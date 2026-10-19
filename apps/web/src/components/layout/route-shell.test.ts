import { matchRoutes } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { isAppOwnedPath, normalizeShellPath } from './route-shell';

describe('shell ownership agrees with registered route semantics', () => {
  const routes = [
    { path: '/quickstart', id: 'app' },
    { path: '/login', id: 'app' },
    { path: '/invite/:token', id: 'app' },
    { path: '/console/*', id: 'app' },
    { path: '*', id: 'public' },
  ];
  it.each([
    '/quickstart',
    '/quickstart/',
    '/QUICKSTART',
    '/models',
    '/login/',
    '/LOGIN',
    '/login/typo',
    '/invite/token',
    '/invite/token/',
    '/invite',
    '/invite/a/b',
    '/console/nope',
    '/consoleish',
    '/unknown',
  ])('%s gets the shell of the route that actually matches', (pathname) => {
    expect(isAppOwnedPath(pathname)).toBe(
      matchRoutes(routes, pathname)?.slice(-1)[0]?.route.id === 'app',
    );
  });
  it('normalizes leaf paths without inventing prefix matches', () => {
    expect(normalizeShellPath('/LOGIN/')).toBe('/login');
    expect(normalizeShellPath('/')).toBe('/');
    expect(isAppOwnedPath('/forbidden/')).toBe(true);
    expect(isAppOwnedPath('/payment/typo')).toBe(false);
  });
});

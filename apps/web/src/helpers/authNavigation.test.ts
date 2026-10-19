import { describe, expect, it } from 'vitest';
import { safeInternalAuthPath, postLoginDestination } from './authNavigation';

const ORIGIN = 'https://console.example.com';

describe('safeInternalAuthPath', () => {
  it('fails closed when no browser origin is available', () => {
    expect(safeInternalAuthPath('/console/wallet')).toBe('/console');
  });

  it('keeps an internal path with its query and fragment', () => {
    expect(safeInternalAuthPath('/console/wallet?range=30#usage', ORIGIN)).toBe(
      '/console/wallet?range=30#usage',
    );
  });

  it.each([
    null,
    '',
    'https://evil.example/steal',
    '//evil.example/steal',
    '/\\evil.example/steal',
    '/\n/evil.example/steal',
    '/\t/evil.example/steal',
  ])('falls back for an unsafe destination: %j', (candidate) => {
    expect(safeInternalAuthPath(candidate, ORIGIN)).toBe('/console');
  });
});

describe('postLoginDestination', () => {
  it.each([
    '/console/organizations',
    '/console/organizations?filter=all',
    '/CONSOLE/ORGANIZATIONS',
    '/console/%6frganizations',
    '/console/user/123',
    '/console/model-ops/sources',
    '/console/channel',
    '/console/search-backend',
    '/console/setting',
    '/console/announcements',
  ])('sends ordinary accounts home from %s', (path) => {
    expect(postLoginDestination(path, 1, ORIGIN)).toBe('/console');
  });
  it.each([
    '/console/wallet?range=30#usage',
    '/console/models',
    '/console/token',
    '/invite/accept?token=example',
  ])('preserves customer destinations: %s', (path) => {
    expect(postLoginDestination(path, 1, ORIGIN)).toBe(path);
  });
  it('lets 管理员 land on every back-office destination', () => {
    expect(postLoginDestination('/console/organizations', 100, ORIGIN)).toBe(
      '/console/organizations',
    );
    expect(postLoginDestination('/console/announcements', '100', ORIGIN)).toBe(
      '/console/announcements',
    );
  });
  it('treats the retired role 10 as an ordinary account', () => {
    expect(postLoginDestination('/console/organizations', 10, ORIGIN)).toBe(
      '/console',
    );
  });
  it.each([undefined, null, 'invalid', {}, Infinity])(
    'does not grant management access for role %j',
    (role) => {
      expect(postLoginDestination('/console/organizations', role, ORIGIN)).toBe(
        '/console',
      );
    },
  );
  it.each([
    '//evil.example',
    '/login?next=/console',
    '/forbidden',
    '/auth/oidc/callback',
    '/console/%zz',
  ])('avoids unsafe destinations and auth loops: %s', (path) => {
    expect(postLoginDestination(path, 1, ORIGIN)).toBe('/console');
  });
});

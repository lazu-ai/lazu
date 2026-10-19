import { describe, expect, it } from 'vitest';
import {
  getRouteTransitionKind,
  isNavigationPathActive,
  isPathWithin,
  isUsageLogsPath,
  resolveConsoleRouteMeta,
} from './routeMeta';

describe('routeMeta', () => {
  it('keeps parent navigation active on nested routes without prefix collisions', () => {
    expect(isPathWithin('/console/model-ops/1', '/console/model-ops')).toBe(
      true,
    );
    expect(isPathWithin('/console/model-ops', '/console/model')).toBe(false);
    expect(isPathWithin('/console', '/console')).toBe(true);
    expect(isNavigationPathActive('/console/wallet', '/console')).toBe(false);
    expect(
      isNavigationPathActive('/console/model-ops/1', '/console/model-ops'),
    ).toBe(true);
  });

  it('resolves specific mobile page identity before the dashboard fallback', () => {
    expect(resolveConsoleRouteMeta('/console/search-backend').match).toBe(
      '/console/search-backend',
    );
    expect(resolveConsoleRouteMeta('/console/model-ops').match).toBe(
      '/console/model-ops',
    );
    expect(resolveConsoleRouteMeta('/console/model-ops').label).toBe(
      'nav.modelOps',
    );
    expect(resolveConsoleRouteMeta('/console/model-ops').fallback).toBe(
      '模型运营',
    );
    expect(resolveConsoleRouteMeta('/console/wallet').label).toBe('nav.wallet');
    expect(resolveConsoleRouteMeta('/console/health').label).toBe(
      'nav.modelStatus',
    );
  });

  it('highlights Logs only for the logs usage view', () => {
    expect(isUsageLogsPath('/console/usage', '')).toBe(true);
    expect(isUsageLogsPath('/console/usage', '?tab=logs')).toBe(true);
    expect(isUsageLogsPath('/console/usage', '?tab=monthly')).toBe(false);
  });

  it('classifies route transitions by surface and history direction', () => {
    expect(getRouteTransitionKind(null, '/console', 'POP')).toBe('none');
    expect(
      getRouteTransitionKind(
        '/console/model-ops',
        '/console/model-ops/1',
        'PUSH',
      ),
    ).toBe('drill-forward');
    expect(
      getRouteTransitionKind('/console/models/1', '/console/models', 'POP'),
    ).toBe('drill-back');
    expect(getRouteTransitionKind('/console', '/quickstart', 'PUSH')).toBe(
      'cross-surface',
    );
    expect(getRouteTransitionKind('/console', '/console/wallet', 'PUSH')).toBe(
      'same-level',
    );
  });
});

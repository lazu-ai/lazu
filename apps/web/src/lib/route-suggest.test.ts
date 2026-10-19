import { describe, expect, it } from 'vitest';
import { suggestRoute } from './route-suggest';

describe('suggestRoute', () => {
  it('corrects a typo in a console address', () => {
    expect(suggestRoute('/console/modles')?.path).toBe('/console/models');
    expect(suggestRoute('/console/tokn')?.path).toBe('/console/token');
    expect(suggestRoute('/Console/Walet/')?.path).toBe('/console/wallet');
  });

  it('corrects a typo in a public address', () => {
    expect(suggestRoute('/modles')?.path).toBe('/models');
    expect(suggestRoute('/privcy')?.path).toBe('/privacy');
  });

  it('points a dead sub-path at the page it runs past', () => {
    expect(suggestRoute('/console/token/abc/def')?.path).toBe('/console/token');
    expect(suggestRoute('/console/model-ops/nope')?.path).toBe(
      '/console/model-ops',
    );
  });

  it('stays silent when nothing is close', () => {
    expect(suggestRoute('/this-does-not-exist')).toBeNull();
    expect(suggestRoute('/console/zzzzzzzz')).toBeNull();
  });

  it('names the page the way the sidebar does', () => {
    expect(suggestRoute('/console/helth')?.label).toBe('nav.modelStatus');
    expect(suggestRoute('/console/modles')?.label).toBe('nav.sidebarModels');
  });
});

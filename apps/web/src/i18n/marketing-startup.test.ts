import { expect, it } from 'vitest';
import { isMarketingStartupPath } from './marketing-startup';

it('eagerly loads marketing for every supported localized home and static page', () => {
  for (const locale of ['', '/zh', '/zh-TW', '/ja']) {
    expect(isMarketingStartupPath(locale || '/')).toBe(true);
    expect(isMarketingStartupPath(`${locale}/`)).toBe(true);
    for (const page of ['about', 'faq', 'privacy', 'terms']) {
      expect(isMarketingStartupPath(`${locale}/${page}`)).toBe(true);
      expect(isMarketingStartupPath(`${locale}/${page}/`)).toBe(true);
    }
  }
});
it('keeps marketing out of application startup', () => {
  for (const path of [
    '/login',
    '/console',
    '/zh/invite/token',
    '/ja/models',
    '/zh-TW/console/personal',
  ])
    expect(isMarketingStartupPath(path)).toBe(false);
});

import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

const routes = {
  keys: 'token',
  usage: 'usage',
  users: 'organizations',
  health: 'health',
  models: 'model-ops',
  wallet: 'wallet',
  settings: 'setting',
  overview: '',
  personal: 'personal',
  announcements: 'announcements',
};
for (const [name, route] of Object.entries(routes)) {
  test(`${name} controls fit in dark desktop and mobile`, async ({ page }) => {
    const app = await setupMockedApp(page, {
      consoleScope: 'platform:operations',
      payloadFor: (url: string) =>
        url.includes('/api/user/self/identities')
          ? { success: true, data: { has_password: true, identities: [] } }
          : undefined,
    });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.setViewportSize({ width: 1280, height: 800 });
    await expectRouteReady(page, `/console/${route}`);
    await page.getByRole('switch', { name: '切换到深色模式' }).click();
    await expect(page.locator('html')).toHaveClass(/dark/);
    await page.screenshot({
      animations: 'disabled',
      path: `../../.local-results/screenshots/implementation/${name}-dark.png`,
    });
    await page.setViewportSize({ width: 390, height: 844 });
    await expect
      .poll(() =>
        page.evaluate(() => document.documentElement.scrollWidth - innerWidth),
      )
      .toBeLessThanOrEqual(1);
    const main = page.getByRole('main');
    const bounds = await main.boundingBox();
    expect(bounds?.width).toBeLessThanOrEqual(391);
    await page.screenshot({
      animations: 'disabled',
      path: `../../.local-results/screenshots/implementation/${name}-mobile.png`,
    });
    await app.expectNoUnexpectedErrors();
  });
}

import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

const routes = {
  keys: 'token',
  usage: 'usage',
  users: 'organizations',
  health: 'health',
  models: 'model-ops',
  search: 'search-backend',
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

test('search editor selectors and actions fit a mobile dialog; outside and Escape cancel', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    consoleScope: 'platform:operations',
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expectRouteReady(page, '/console/search-backend');
  await page.getByRole('button', { name: '添加搜索服务', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  const bounds = await dialog.boundingBox();
  expect(bounds?.x).toBeGreaterThanOrEqual(0);
  expect((bounds?.x || 0) + (bounds?.width || 0)).toBeLessThanOrEqual(390);
  const select = dialog.getByRole('combobox').first();
  await select.click();
  await expect(page.getByRole('listbox')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(dialog).toBeVisible();
  await page.screenshot({
    animations: 'disabled',
    path: '../../.local-results/screenshots/implementation/search-editor-mobile.png',
  });
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await app.expectNoUnexpectedErrors();
});

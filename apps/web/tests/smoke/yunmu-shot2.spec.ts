// @ts-nocheck
/** Scratch: screenshots for the account-menu targets + quickstart. */
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

const routes = [
  ['personal', '/console/personal'],
  ['wallet', '/console/wallet'],
  ['usage-monthly', '/console/usage?tab=monthly'],
  ['setting-accounts', '/console/setting?tab=accounts'],
  ['setting-observability', '/console/setting?tab=observability'],
];

for (const [name, path] of routes) {
  test(`shot2 ${name}`, async ({ page }) => {
    await page.setViewportSize({ height: 940, width: 1440 });
    await setupMockedApp(page);
    await expectRouteReady(page, path);
    await page.waitForTimeout(700);
    await page.screenshot({
      path: `.local-results/yunmu2-${name}.png`,
      fullPage: false,
    });
  });
}

test('shot2 compact sidebar footer', async ({ page }) => {
  await page.setViewportSize({ height: 940, width: 1440 });
  await setupMockedApp(page);
  await expectRouteReady(page, '/console');
  await expect(page.locator('.sidebar-v2__account')).toHaveCount(0);
  await expect(page.locator('.sidebar-v2__logout')).toBeVisible();
  await page.waitForTimeout(400);
  await page.screenshot({ path: '.local-results/yunmu2-sidebar-footer.png' });
});

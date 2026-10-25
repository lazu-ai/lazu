// @ts-nocheck
/* /api/status is a cached query: moving between public and console pages
 * does not read it again while it is fresh, and saving a setting reads it
 * again at once, so the console shows the new name, logo or limits. */
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

const countStatus = (page) => {
  const reads = { count: 0 };
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/api/status') reads.count += 1;
  });
  return reads;
};

const navigateInApp = (page, path: string) =>
  page.evaluate((to) => {
    window.history.pushState({}, '', to);
    window.dispatchEvent(new PopStateEvent('popstate'));
  }, path);

test('moving between public and console pages reads /api/status once', async ({
  page,
}) => {
  await setupMockedApp(page, { language: 'zh' });
  const reads = countStatus(page);
  await expectRouteReady(page, '/console/token');
  await expect.poll(() => reads.count).toBe(1);
  await navigateInApp(page, '/');
  await expect(page).toHaveURL(/\/$/);
  await navigateInApp(page, '/console/token');
  await expect(page).toHaveURL(/\/console\/token$/);
  await page.waitForTimeout(1000);
  expect(reads.count).toBe(1);
});

test('saving a setting reads /api/status again', async ({ page }) => {
  const values: Record<string, string> = {
    PreConsumedQuota: '0',
    QuotaForNewUser: '0',
  };
  await setupMockedApp(page, {
    payloadFor: (url: string, request: Request) => {
      if (new URL(url).pathname !== '/api/option/') return undefined;
      if (request.method() === 'PUT') {
        const entry = request.postDataJSON() as { key: string; value: string };
        values[entry.key] = entry.value;
        return { success: true };
      }
      return {
        success: true,
        data: Object.entries(values).map(([key, value]) => ({ key, value })),
      };
    },
  });
  const reads = countStatus(page);
  await expectRouteReady(page, '/console/setting?tab=billing');
  await expect.poll(() => reads.count).toBe(1);
  await page.locator('#QuotaForNewUser').fill('12345');
  await page
    .locator('.set-unsaved')
    .getByRole('button', { name: '保存', exact: true })
    .click();
  await expect.poll(() => values.QuotaForNewUser).toBe('12345000000');
  await expect.poll(() => reads.count).toBe(2);
});

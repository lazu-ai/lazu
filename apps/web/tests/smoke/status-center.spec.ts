// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

test('status history is isolated by account and active scope', async ({
  page,
}) => {
  const app = await setupMockedApp(page);
  await page.addInitScript(() => {
    const issue = {
      count: 1,
      firstSeenAt: Date.now(),
      id: 'error:Other account:private failure',
      kind: 'error',
      lastSeenAt: Date.now(),
      message: 'private failure',
      source: 'Other account',
    };
    localStorage.setItem('lazu-status-center', JSON.stringify([issue]));
    localStorage.setItem(
      'lazu-status-center:2:workspace:default',
      JSON.stringify([issue]),
    );
  });
  await expectRouteReady(page, '/console');

  await expect(page.locator('.lazu-status-center__count')).toHaveCount(0);
  await page.evaluate(() => window.dispatchEvent(new Event('offline')));
  await expect(page.locator('.lazu-status-center__count')).toHaveText('1');

  const stored = await page.evaluate(() => ({
    current: localStorage.getItem('lazu-status-center:1:workspace:default'),
    legacy: localStorage.getItem('lazu-status-center'),
    other: localStorage.getItem('lazu-status-center:2:workspace:default'),
  }));
  expect(stored.current).toContain('This device is offline');
  expect(stored.legacy).toBeNull();
  expect(stored.other).toContain('private failure');
  await app.expectNoUnexpectedErrors();
});

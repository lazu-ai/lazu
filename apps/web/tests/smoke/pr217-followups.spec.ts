import { expect, test } from '@playwright/test';
import {
  expectRouteReady,
  mockOverview,
  setupMockedApp,
} from './helpers/mock-app';

test('mobile setting controls stack below their labels', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const app = await setupMockedApp(page);
  await expectRouteReady(page, '/console/setting');
  const rows = page
    .locator('.set-row:visible')
    .filter({ has: page.locator('.set-row__vv') });
  expect(await rows.count()).toBeGreaterThan(0);
  for (const row of await rows.all()) {
    const label = await row.locator('.set-row__lb').boundingBox();
    const value = await row.locator('.set-row__vv').boundingBox();
    if (label && value)
      expect(value.y).toBeGreaterThanOrEqual(label.y + label.height - 1);
  }
  await page.screenshot({
    path: '../../.local-results/screenshots/pr217-followups/settings-mobile.png',
    fullPage: true,
  });
  await app.expectNoUnexpectedErrors();
});

test('legal chapters keep distinct decoded anchors and add no governing clause', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const app = await setupMockedApp(page, {
    payloadFor: (url: string) =>
      new URL(url).pathname === '/api/privacy-policy'
        ? {
            success: true,
            data: '# Privacy\n\nLast updated: June 1, 2026\n\n## 1. Data &amp; privacy\n\nFirst chapter.\n\n## 1. Data &amp; privacy\n\nSecond chapter.',
          }
        : undefined,
  });
  await expectRouteReady(page, '/privacy');
  const headings = page.locator('.legal-doc h2');
  await expect(headings).toHaveCount(2);
  const ids = await headings.evaluateAll((nodes) =>
    nodes.map((node) => node.id),
  );
  expect(new Set(ids).size).toBe(2);
  expect(ids.every(Boolean)).toBe(true);
  await expect(headings.first()).toContainText('Data & privacy');
  await expect(page.locator('.legal-foot')).not.toContainText('简体中文为准');
  await expect(page.locator('.legal-anchors')).toHaveCSS('align-self', 'start');
  await page.screenshot({
    path: '../../.local-results/screenshots/pr217-followups/legal.png',
    fullPage: true,
  });
  await app.expectNoUnexpectedErrors();
});

test('a complete routing outage links to failures in the dashboard window', async ({
  page,
}) => {
  const app = await setupMockedApp(page, {
    user: { role: 1 },
    payloadFor: (url: string) =>
      new URL(url).pathname === '/api/data/overview'
        ? {
            success: true,
            data: mockOverview({
              totals: {
                ...mockOverview().totals,
                outcome_count: 20,
                error_count: 20,
                request_count: 0,
                success_rate: 0,
              },
            }),
          }
        : undefined,
  });
  await expectRouteReady(page, '/console');
  const failures = page
    .locator('.ov-needs a')
    .filter({ hasText: '次请求失败' });
  await expect(failures).toBeVisible();
  const href = new URL(
    (await failures.getAttribute('href'))!,
    'http://localhost',
  );
  expect(href.searchParams.get('type')).toBe('5');
  expect(Number(href.searchParams.get('start_timestamp'))).toBeGreaterThan(0);
  expect(Number(href.searchParams.get('end_timestamp'))).toBeGreaterThan(
    Number(href.searchParams.get('start_timestamp')),
  );
  await app.expectNoUnexpectedErrors();
});

test('key usage queries immutable identity and labels expired keys', async ({
  page,
}) => {
  const ids: string[] = [];
  const app = await setupMockedApp(page, {
    payloadFor: (url: string) => {
      const request = new URL(url);
      if (
        request.pathname === '/api/token/' ||
        request.pathname === '/api/token'
      )
        return {
          success: true,
          data: {
            items: [
              {
                id: 'tok_test_owner',
                name: 'prod',
                status: 3,
                expired_time: 1700000000,
                used_quota: 0,
                remain_quota: 0,
              },
            ],
            total: 30,
            page: 1,
            page_size: 10,
          },
        };
      if (request.pathname === '/api/log/self/stat') {
        ids.push(request.searchParams.get('token_id') || '');
        expect(request.searchParams.has('token_name')).toBe(false);
        return { success: true, data: { amount_microusd: 1000000 } };
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/token');
  await page
    .getByRole('row', { name: /prod/ })
    .locator('.lazu-table__expand-toggle')
    .click();
  await expect(page.locator('.tok-usage')).toBeVisible();
  await expect.poll(() => ids.length).toBe(2);
  expect(ids).toEqual(['tok_test_owner', 'tok_test_owner']);
  await expect(page.locator('.tok-usage')).toContainText('已过期');
  await expect(page.locator('.tok-usage')).not.toContainText('剩 -');
  await app.expectNoUnexpectedErrors();
});

test('wallet restores custom checkout amounts and wraps its mobile equation', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const app = await setupMockedApp(page, {
    status: { enable_stripe_topup: true },
  });
  await page.addInitScript(() =>
    localStorage.setItem('lazu:wallet:last-selected-amount', '42'),
  );
  await expectRouteReady(page, '/console/wallet');
  await expect(page.locator('#wallet-custom-amount')).toHaveValue('42');
  await expect(page.locator('.wal-balance-equation')).toHaveCSS(
    'display',
    'grid',
  );
  await page.locator('#wallet-custom-amount').fill('43');
  await expect(page.locator('#wallet-custom-amount')).toHaveValue('43');
  await page.screenshot({
    path: '../../.local-results/screenshots/pr217-followups/wallet-mobile.png',
    fullPage: true,
  });
  await app.expectNoUnexpectedErrors();
});

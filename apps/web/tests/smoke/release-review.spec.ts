import { expect, test } from '@playwright/test';
import {
  expectRouteReady,
  mockOverview,
  setupMockedApp,
} from './helpers/mock-app';

test("platform user margin drills into that user's usage by identity", async ({
  page,
}) => {
  await setupMockedApp(page, {
    consoleScope: 'platform:operations',
    payloadFor: (url: string) =>
      new URL(url).pathname === '/api/data/overview'
        ? {
            success: true,
            message: '',
            data: mockOverview({
              users: [
                {
                  id: 42,
                  name: 'renamed & reused',
                  request_count: 12,
                  amount_microusd: 24_000_000,
                  gross_margin_microusd: 6_000_000,
                  prompt_tokens: 0,
                  completion_tokens: 0,
                  cached_input_tokens: 0,
                  last_used_at: 0,
                },
              ],
            }),
          }
        : undefined,
  });
  await expectRouteReady(page, '/console');
  const logs = page.waitForRequest(
    (r) =>
      new URL(r.url()).pathname === '/api/log/' &&
      new URL(r.url()).searchParams.get('user_id') === '42',
  );
  await page.getByRole('link', { name: 'renamed & reused' }).click();
  await logs;
  const url = new URL(page.url());
  expect(url.searchParams.get('user_id')).toBe('42');
  expect(url.searchParams.get('owner')).toBe('renamed & reused');
});

test('panel shadows retain their light and dark recipes after canonical token migration', async ({
  page,
}) => {
  await setupMockedApp(page, { consoleScope: 'platform:operations' });
  await expectRouteReady(page, '/console');
  // Pages restyle their own cards; a bare probe reads the canonical recipe.
  await page.evaluate(() => {
    const probe = document.createElement('div');
    probe.className = 'lazu-data-card';
    probe.id = 'shadow-probe';
    document.body.appendChild(probe);
  });
  const panel = page.locator('#shadow-probe');
  await expect(panel).toHaveCSS(
    'box-shadow',
    /rgba\(50, 56, 70, 0\.05\).*rgba\(50, 56, 70, 0\.08\)/,
  );
  await page.getByRole('switch', { name: '切换到深色模式' }).click();
  await expect(panel).toHaveCSS(
    'box-shadow',
    'rgba(0, 0, 0, 0.38) 0px 12px 30px 0px',
  );
});

for (const role of [10, 100]) {
  test(`announcement draft tasks respect role ${role}`, async ({ page }) => {
    const announcementRequests: string[] = [];
    await setupMockedApp(page, {
      consoleScope: 'platform:operations',
      user: { role },
      payloadFor: (url: string) =>
        new URL(url).pathname === '/api/admin/announcements'
          ? {
              success: true,
              message: '',
              data: { items: [{ id: 1, published_at: null }] },
            }
          : undefined,
    });
    page.on('request', (request) => {
      if (new URL(request.url()).pathname === '/api/admin/announcements')
        announcementRequests.push(request.url());
    });
    await expectRouteReady(page, '/console');
    const task = page.locator('.ov-needs a[href="/console/ops/notices"]');
    if (role === 100) {
      await expect(task).toBeVisible();
      await expect.poll(() => announcementRequests.length).toBeGreaterThan(0);
    } else {
      await expect(task).toHaveCount(0);
      expect(announcementRequests).toHaveLength(0);
    }
  });
}

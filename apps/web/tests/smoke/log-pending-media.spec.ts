// @ts-nocheck
import { expect, test } from '@playwright/test';
import { expectRouteReady, setupMockedApp } from './helpers/mock-app';

const now = Math.floor(Date.now() / 1000);

const pendingVideo = {
  id: 901,
  type: 2,
  created_at: now - 100,
  model_name: 'seedance-2.5',
  token_name: 'video-prod',
  request_id: 'req_lazu_pending',
  amount_microusd: 0,
  content: 'media_job media job video mjob_pending submitted',
  other: JSON.stringify({
    source: 'media_job',
    media_job_id: 'mjob_pending',
    kind: 'video',
    pending: true,
    hold_microusd: 1155000,
  }),
};

test('a video job shows in the log as in progress from the moment it is accepted', async ({
  page,
}) => {
  const logQueries = [];
  const app = await setupMockedApp(page, {
    payloadFor: (url) => {
      const { pathname } = new URL(url);
      if (pathname === '/api/log/self/') {
        logQueries.push(Date.now());
        return {
          data: { items: [pendingVideo], page: 1, page_size: 10, total: 1 },
          message: '',
          success: true,
        };
      }
      return undefined;
    },
  });
  await expectRouteReady(page, '/console/usage?tab=logs');

  const row = page.locator('.logv2-desktop-master tbody tr').first();
  await expect(row).toContainText('执行中');
  await expect(row).toContainText('预扣 $1.155');
  await expect(row).toContainText('已等');
  if (process.env.LOG_SCREENSHOT) {
    await page.screenshot({ path: process.env.LOG_SCREENSHOT });
  }
  // While a row is in progress the log keeps looking for its settlement.
  await expect
    .poll(() => logQueries.length, { timeout: 15_000 })
    .toBeGreaterThan(1);

  await app.expectNoUnexpectedErrors();
});
